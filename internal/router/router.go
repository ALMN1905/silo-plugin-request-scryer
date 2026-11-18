// Package router implements Silo's request_router.v1 capability with Scryer:
// an approved request becomes a monitored title in Scryer, which then searches
// for it and hands the release to its download client.
package router

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/neilmc78/silo-plugin-request-scryer/internal/scryer"
	"github.com/neilmc78/silo-plugin-request-scryer/internal/seasonmap"
	"github.com/neilmc78/silo-plugin-request-scryer/internal/tmdb"
)

// Host-normalized target statuses.
const (
	statusQueued      = "queued"
	statusDownloading = "downloading"
	statusCompleted   = "completed"
	statusFailed      = "failed"
)

// Router serves RequestRouter.
type Router struct {
	pluginv1.UnimplementedRequestRouterServer

	// newClient builds the Scryer client for a connection. Tests point it at a
	// fake Scryer.
	newClient func(conn *pluginv1.RouterConnection) *scryer.Client
	now       func() time.Time

	// tmdbBase is TMDB's address, changed by tests.
	tmdbBase string
	tmdb     sync.Map // tmdb id -> cachedSeasons
}

var _ pluginv1.RequestRouterServer = (*Router)(nil)

// New returns a router that talks to the Scryer servers Silo passes in.
func New() *Router {
	return &Router{
		newClient: func(conn *pluginv1.RouterConnection) *scryer.Client {
			return scryer.New(conn.GetBaseUrl(), conn.GetApiKey())
		},
		now: time.Now,
	}
}

// Fulfill adds the title to Scryer, monitored, and starts a search. Silo
// routes each quality tier to one server; Scryer's own quality profile decides
// the release, so every requested tier maps to the same Scryer title.
//
// A connection or key failure returns an error so Silo retries the submission.
// A failure Scryer explains (an unknown ID, say) fails the target instead,
// since retrying the same call would fail the same way.
func (r *Router) Fulfill(ctx context.Context, req *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	resp := &pluginv1.FulfillResponse{}
	conns := req.GetConnections()
	if len(conns) == 0 {
		resp.Message = "no Scryer server configured"
		return resp, nil
	}
	conn := conns[0]
	client := r.newClient(conn)

	qualities := req.GetQualities()
	if len(qualities) == 0 {
		qualities = []*pluginv1.RequestedQuality{{}}
	}
	target := func(q *pluginv1.RequestedQuality, titleID, status, message string) *pluginv1.FulfillmentTarget {
		return &pluginv1.FulfillmentTarget{
			Quality:        q.GetId(),
			ConnectionId:   conn.GetId(),
			ExternalId:     titleID,
			Status:         status,
			ExternalStatus: status,
			Message:        message,
		}
	}

	d := req.GetRequest()
	series := d.GetMediaType() == "series"
	if series && tmdbKey(conn) == "" {
		for _, q := range qualities {
			resp.Targets = append(resp.Targets, target(q, "", statusFailed, "Set a TMDB API key in the plugin settings: a series request needs it to match Silo's seasons to Scryer's."))
		}
		return resp, nil
	}

	// A series request names the seasons wanted; one without any (TMDB lists no
	// aired season yet) asks for the whole series.
	seasons := d.GetSeasons()
	whole := series && len(seasons) == 0
	input := addTitleInput(d, seriesMonitorType(conn))
	if series && !whole {
		// The seasons are switched on once the title exists, so it is added
		// unmonitored and without monitor options (see MonitorSeasons).
		input.Monitored = false
		input.Options = nil
	}
	var gqlErr *scryer.GraphQLError
	// Silo's season numbers are TMDB's. They are read before anything is added,
	// so a TMDB failure leaves Scryer untouched and the request can be retried.
	var theirs []seasonmap.Season
	if series && !whole {
		var err error
		switch theirs, err = r.tmdbSeasonsOf(ctx, conn, d); {
		case errors.Is(err, tmdb.ErrKeyRefused), errors.Is(err, tmdb.ErrNotFound):
			for _, q := range qualities {
				resp.Targets = append(resp.Targets, target(q, "", statusFailed, "Could not match the seasons: "+err.Error()))
			}
			return resp, nil
		case err != nil:
			return nil, err // Silo retries the request
		}
	}
	var libraryNote string
	if choice := animeMovieLibrary(conn); choice != "" && isAnimeMovie(d) {
		id, err := client.LibraryID(ctx, "MOVIE", choice)
		switch {
		case err == nil:
			input.LibraryID = &id
		case errors.As(err, &gqlErr), errors.Is(err, scryer.ErrLibraryNotFound):
			// The library was deleted, or Scryer refused to list libraries. The
			// request still goes through, to the default movie library.
			libraryNote = " The chosen anime movies library could not be used; the title went to the default movie library."
		default:
			// Scryer could not be reached: Silo retries the request.
			return nil, err
		}
	}
	failed := func(titleID, message string) *pluginv1.FulfillResponse {
		for _, q := range qualities {
			resp.Targets = append(resp.Targets, target(q, titleID, statusFailed, message))
		}
		return resp
	}

	// The monitor type options of a whole-series add would replace the settings
	// of a title Scryer already has, so such a title is looked for first and left
	// as it is.
	if whole {
		id, err := client.FindTitle(ctx, input.Facet, d.GetExternalIds()["tmdb"])
		switch {
		case errors.As(err, &gqlErr):
			return failed("", "Scryer could not look the title up: "+gqlErr.Error()), nil
		case err != nil:
			return nil, err
		case id != "":
			for _, q := range qualities {
				resp.Targets = append(resp.Targets, target(q, id, statusQueued, "Already in Scryer; nothing to change."))
			}
			return resp, nil
		}
	}

	// Scryer refuses a tag that is not defined, so define the tags first. A
	// tag Scryer will not define is left off and the others are still used;
	// Scryer being unreachable retries the request instead, so the title is
	// not added untagged for good.
	tags := requestTags(conn, d)
	var tagNote string
	if len(tags) > 0 {
		use, dropped, err := client.EnsureTags(ctx, tags)
		if err != nil {
			return nil, err
		}
		if len(dropped) > 0 {
			tagNote = " Not tagged " + strings.Join(dropped, ", ") + ": Scryer would not define the tag (the API key needs the catalog-settings permission)."
		}
		tags = use
	}
	input.Tags = append(input.Tags, tags...)

	added, err := client.AddTitle(ctx, input)
	switch {
	case errors.As(err, &gqlErr):
		return failed("", "Scryer could not add it: "+gqlErr.Error()), nil
	case err != nil:
		return nil, err
	}
	titleID := added.ID

	// changed: the plugin changed something in Scryer, so the title is tagged
	// and searched. A new title is a change; for one Scryer already had it only
	// counts when the plugin had to switch something on.
	changed := !added.Reused
	hasFile := false
	switch {
	case !series && added.Reused:
		// A movie Scryer already has is left alone when it has a file; without
		// one it has to be monitored, or the request would never download.
		title, err := client.Title(ctx, titleID)
		switch {
		case err != nil:
			return nil, err
		case title == nil:
			return failed(titleID, "The title was removed from Scryer."), nil
		case len(title.MediaFiles) > 0:
			hasFile = true
		case !title.Monitored:
			if err := client.SetTitleMonitored(ctx, titleID); err != nil {
				if errors.As(err, &gqlErr) {
					return failed(titleID, "Scryer could not monitor it: "+gqlErr.Error()), nil
				}
				return nil, err
			}
			changed = true
		}
	case series && !whole:
		mapped, err := matchSeasons(ctx, client, titleID, seasons, theirs, !added.Reused)
		var switched bool
		if err == nil {
			switched, err = client.MonitorSeasons(ctx, titleID, mapped, !added.Reused)
		}
		switch {
		case errors.As(err, &gqlErr), errors.Is(err, scryer.ErrSeasonNotFound):
			return failed(titleID, "Scryer could not monitor the seasons: "+err.Error()), nil
		case err != nil:
			return nil, err // Silo retries the request
		}
		changed = changed || switched
	}

	// Scryer has no setting for the specials of a series, so a new series gets
	// them monitored only when the admin asks. Anime follow Scryer's own anime
	// setting, and a title Scryer already had keeps its own.
	if series && !d.GetIsAnime() && !added.Reused && monitorSeriesSpecials(conn) {
		if err := client.MonitorSpecials(ctx, titleID); err != nil {
			if errors.As(err, &gqlErr) {
				return failed(titleID, "Scryer could not monitor the specials: "+gqlErr.Error()), nil
			}
			return nil, err
		}
	}

	// Scryer ignores the tags of an addTitle that finds the title already in the
	// library, so a title the plugin changed is tagged afterwards.
	if added.Reused && changed && len(tags) > 0 {
		if err := client.UpdateTitleTags(ctx, titleID, tags); err != nil {
			tagNote = " Not tagged: " + err.Error() + "."
		}
	}

	message := "Added to Scryer; searching for a release."
	switch {
	case added.Reused && changed:
		message = "Monitored in Scryer; searching for a release."
	case added.Reused && series, added.Reused && hasFile:
		message = "Already in Scryer; nothing to change."
	case added.Reused:
		message = "Already in Scryer; searching for a release."
	}
	if !added.Reused || changed || (added.Reused && !series && !hasFile) {
		if err := client.Search(ctx, titleID); err != nil {
			// The title is monitored, so Scryer's RSS sync still finds it.
			message = "Added to Scryer, but the search did not start; Scryer will pick it up from its RSS sync."
		}
	}
	message += libraryNote + tagNote
	for _, q := range qualities {
		resp.Targets = append(resp.Targets, target(q, titleID, statusQueued, message))
	}
	return resp, nil
}

// configAnimeMoviesLibrary is the connection config key of the dropdown that
// picks the Scryer movie library for anime movies. Its value is the library's
// id, so renaming the library in Scryer does not break the choice; an empty
// value means the default movie library.
const configAnimeMoviesLibrary = "anime_movies_library"

func isAnimeMovie(d *pluginv1.RequestDescriptor) bool {
	return d.GetMediaType() == "movie" && d.GetIsAnime()
}

// animeMovieLibrary returns the chosen library for anime movies, or "" when
// none is chosen (anime movies then go to Scryer's default movie library).
func animeMovieLibrary(conn *pluginv1.RouterConnection) string {
	return strings.TrimSpace(conn.GetConfig().GetFields()[configAnimeMoviesLibrary].GetStringValue())
}

// configSeriesMonitorType is the connection config key for how a requested
// series or anime is monitored.
const configSeriesMonitorType = "series_monitor_type"

// seriesMonitorTypes are the Scryer MonitorTypeValue names offered for series.
// UNMONITORED would make a request do nothing, ADVANCED needs a season
// selection and NONE is not a monitoring choice.
var seriesMonitorTypes = map[string]bool{
	"ALL_EPISODES":                true,
	"MISSING_AND_FUTURE_EPISODES": true,
	"FUTURE_EPISODES":             true,
}

// seriesMonitorType returns the monitoring to send for a series or anime, or
// "" to send none. Nothing is sent unless the admin picked a type: Scryer
// applies a sent type to a title it already has too (replacing its monitor
// type and clearing any season selection), so sending one by default would
// change the monitoring of every series that is requested again.
func seriesMonitorType(conn *pluginv1.RouterConnection) string {
	v := strings.TrimSpace(conn.GetConfig().GetFields()[configSeriesMonitorType].GetStringValue())
	if seriesMonitorTypes[v] {
		return v
	}
	return ""
}

// configSeriesSpecials is the connection config key of the switch that monitors
// the specials of a new series; absent means off, as in Scryer.
const configSeriesSpecials = "monitor_series_specials"

func monitorSeriesSpecials(conn *pluginv1.RouterConnection) bool {
	return conn.GetConfig().GetFields()[configSeriesSpecials].GetBoolValue()
}

// configTMDBKey is the connection config key of the TMDB API key. Silo numbers
// seasons as TMDB does, so a series request cannot be matched to Scryer's seasons
// without it.
const configTMDBKey = "tmdb_api_key"

func tmdbKey(conn *pluginv1.RouterConnection) string {
	return strings.TrimSpace(conn.GetConfig().GetFields()[configTMDBKey].GetStringValue())
}

// tmdbTTL is how long a series' TMDB seasons are kept.
const tmdbTTL = 12 * time.Hour

type cachedSeasons struct {
	seasons []seasonmap.Season
	until   time.Time
}

// tmdbSeasonsOf reads the seasons of the requested series from TMDB; nil when
// the request carries no TMDB id.
func (r *Router) tmdbSeasonsOf(ctx context.Context, conn *pluginv1.RouterConnection, d *pluginv1.RequestDescriptor) ([]seasonmap.Season, error) {
	tmdbID := d.GetExternalIds()["tmdb"]
	if tmdbID == "" {
		return nil, nil
	}
	return r.tmdbSeasons(ctx, tmdbKey(conn), tmdbID)
}

// matchSeasons turns the TMDB season numbers Silo asked for into Scryer season
// numbers, by air date and episode count (see seasonmap). With no TMDB seasons
// the numbers are used as they are.
func matchSeasons(ctx context.Context, client *scryer.Client, titleID string, seasons []int32, theirs []seasonmap.Season, newTitle bool) ([]int32, error) {
	if theirs == nil || len(seasons) == 0 {
		return seasons, nil
	}
	ours, err := client.SeasonStarts(ctx, titleID, newTitle)
	if err != nil {
		return nil, err
	}
	return seasonmap.Map(seasons, theirs, ours), nil
}

func (r *Router) tmdbSeasons(ctx context.Context, key, tmdbID string) ([]seasonmap.Season, error) {
	if v, ok := r.tmdb.Load(tmdbID); ok && r.now().Before(v.(cachedSeasons).until) {
		return v.(cachedSeasons).seasons, nil
	}
	seasons, err := tmdb.New(r.tmdbBase, key).Seasons(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	r.tmdb.Store(tmdbID, cachedSeasons{seasons: seasons, until: r.now().Add(tmdbTTL)})
	return seasons, nil
}

// configTagTitles is the connection config key of the switch that tags the
// titles the plugin adds; absent means off, so nothing changes until the admin
// chooses it.
const configTagTitles = "tag_titles"

// requestTags returns the tags for a requested title: a common one to find
// every Silo request, and one for the requester. Scryer stores labels lowercase
// with whitespace collapsed, so they are built that way.
func requestTags(conn *pluginv1.RouterConnection, d *pluginv1.RequestDescriptor) []string {
	if !conn.GetConfig().GetFields()[configTagTitles].GetBoolValue() {
		return nil
	}
	tags := []string{"silo-request"}
	if user := userTag(d); user != "" {
		tags = append(tags, user)
	}
	return tags
}

// maxUserTagLength caps the username part of a requester tag.
const maxUserTagLength = 40

// userTag returns the requester's tag, silo-user-<name>, or "" when the
// request names nobody. Silo usernames are free text and can be email
// addresses, so the name keeps to lowercase letters, digits and single
// dashes, an address is cut at the @ so it never ends up in Scryer, and a
// name with nothing usable left falls back to the Silo account id. The tag
// names the account: every profile on it shares the tag, and renaming the
// account starts a new one.
func userTag(d *pluginv1.RequestDescriptor) string {
	name := strings.ToLower(strings.TrimSpace(d.GetRequesterUsername()))
	if at := strings.IndexByte(name, '@'); at > 0 {
		name = name[:at]
	}
	var b strings.Builder
	dash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > maxUserTagLength {
		slug = strings.TrimRight(slug[:maxUserTagLength], "-")
	}
	if slug == "" {
		id := d.GetRequesterUserId()
		if id <= 0 {
			return ""
		}
		slug = strconv.FormatInt(id, 10)
	}
	return "silo-user-" + slug
}

// shorten cuts s to at most n bytes, on a rune boundary.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func addTitleInput(d *pluginv1.RequestDescriptor, monitorType string) scryer.AddTitle {
	in := scryer.AddTitle{
		Name:      d.GetTitle(),
		Facet:     "MOVIE",
		Monitored: true,
		Tags:      []string{},
	}
	if d.GetMediaType() == "series" {
		in.Facet = "SERIES"
		if d.GetIsAnime() {
			in.Facet = "ANIME"
		}
		if monitorType != "" {
			in.Options = &scryer.TitleOptions{MonitorType: monitorType}
		}
	}
	if y := int(d.GetYear()); y > 0 {
		in.Year = &y
	}
	ids := d.GetExternalIds()
	if v, err := strconv.ParseInt(strings.TrimSpace(ids["tmdb"]), 10, 64); err == nil && v > 0 {
		in.TmdbID = &v
	}
	if v := strings.TrimSpace(ids["tvdb"]); v != "" && v != "0" {
		in.TvdbID = &v
	}
	if v := strings.TrimSpace(ids["imdb"]); strings.HasPrefix(v, "tt") {
		in.ImdbID = &v
	}
	return in
}

// CheckStatus reports each target from its Scryer title: downloading while
// Scryer has downloads in flight, completed once it has files, queued while it
// is still looking, failed once the title is gone from Scryer.
func (r *Router) CheckStatus(ctx context.Context, req *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	conns := map[string]*pluginv1.RouterConnection{}
	for _, c := range req.GetConnections() {
		conns[c.GetId()] = c
	}
	resp := &pluginv1.CheckStatusResponse{}
	titles := map[string]*scryer.Title{}
	for _, t := range req.GetTargets() {
		st := &pluginv1.TargetStatus{Quality: t.GetQuality(), ConnectionId: t.GetConnectionId()}
		conn, ok := conns[t.GetConnectionId()]
		if !ok || t.GetExternalId() == "" {
			st.Status, st.Message = statusFailed, "The Scryer server for this request is gone."
			resp.Statuses = append(resp.Statuses, st)
			continue
		}
		key := t.GetConnectionId() + "/" + t.GetExternalId()
		title, seen := titles[key]
		if !seen {
			var err error
			title, err = r.newClient(conn).Title(ctx, t.GetExternalId())
			if err != nil {
				// Silo keeps the target as it is and asks again next pass.
				return nil, err
			}
			titles[key] = title
		}
		var seasons []int32
		if d := req.GetRequest(); d.GetMediaType() == "series" {
			seasons = d.GetSeasons()
			// The status only needs the seasons; if they cannot be matched now
			// (TMDB down, say) the numbers are used rather than guessing a failure.
			if theirs, err := r.tmdbSeasonsOf(ctx, conn, d); err == nil {
				if mapped, err := matchSeasons(ctx, r.newClient(conn), t.GetExternalId(), seasons, theirs, false); err == nil {
					seasons = mapped
				}
			}
		}
		r.describe(st, title, seasons)
		resp.Statuses = append(resp.Statuses, st)
	}
	return resp, nil
}

// seasonsHaveFiles reports whether every requested season has at least one
// file in Scryer.
func seasonsHaveFiles(title *scryer.Title, seasons []int32) bool {
	for _, n := range seasons {
		owned := false
		for _, col := range title.Collections {
			if col.Index == strconv.Itoa(int(n)) && col.EpisodesOwned != nil && *col.EpisodesOwned > 0 {
				owned = true
			}
		}
		if !owned {
			return false
		}
	}
	return true
}

func (r *Router) describe(st *pluginv1.TargetStatus, title *scryer.Title, seasons []int32) {
	switch {
	case title == nil:
		st.Status, st.Message = statusFailed, "The title was removed from Scryer."
	case progressOf(title.Queue, r.now()) != nil:
		st.Status = statusDownloading
		st.Progress = progressOf(title.Queue, r.now())
	case len(seasons) > 0 && seasonsHaveFiles(title, seasons):
		st.Status = statusCompleted
	case len(seasons) == 0 && len(title.MediaFiles) > 0:
		st.Status = statusCompleted
	case !title.Monitored:
		st.Status = statusQueued
		st.Wording = &pluginv1.RequestStatusWording{
			Label:  "Not monitored",
			Detail: "Scryer isn't looking for it. Monitor it in Scryer to resume.",
		}
	default:
		st.Status = statusQueued
	}
	st.ExternalStatus = st.Status
}

// phaseRank orders download phases as the host combines them.
var phaseRank = map[string]int{
	"import_blocked": 6, "stalled": 5, "downloading": 4, "importing": 3, "paused": 2, "queued": 1,
}

// progressOf sums Scryer's live downloads for a title, or returns nil when
// none are in flight.
func progressOf(items []scryer.QueueItem, now time.Time) *pluginv1.DownloadProgress {
	p := &pluginv1.DownloadProgress{}
	sizeKnown := true
	var latest time.Time
	for _, item := range items {
		phase := phaseOf(item.DisplayState)
		if phase == "" {
			continue
		}
		p.Downloads++
		if phaseRank[phase] > phaseRank[p.Phase] {
			p.Phase = phase
		}
		if item.SizeBytes == nil || *item.SizeBytes <= 0 {
			sizeKnown = false
		} else {
			done := min(max(item.ProgressPercent, 0), 100)
			p.BytesTotal += *item.SizeBytes
			p.BytesLeft += *item.SizeBytes * int64(100-done) / 100
		}
		if item.RemainingSeconds != nil && *item.RemainingSeconds > 0 {
			if eta := now.Add(time.Duration(*item.RemainingSeconds) * time.Second); eta.After(latest) {
				latest = eta
			}
		}
	}
	if p.Downloads == 0 {
		return nil
	}
	if !sizeKnown {
		p.BytesTotal, p.BytesLeft = 0, 0
	}
	if !latest.IsZero() {
		p.EstimatedCompletion = timestamppb.New(latest)
	}
	return p
}

// phaseOf maps Scryer's download display state to the host's phases; "" means
// the item is not a live download for the title.
func phaseOf(displayState string) string {
	switch displayState {
	case "QUEUED":
		return "queued"
	case "DOWNLOADING":
		return "downloading"
	case "PAUSED":
		return "paused"
	case "POST_PROCESSING", "COMPLETED", "IMPORTING", "IMPORT_PENDING":
		return "importing"
	case "WARNING", "FAILED":
		return "stalled"
	case "IMPORT_BLOCKED", "IMPORT_FAILED":
		return "import_blocked"
	}
	return "" // IMPORTED_SEEDING, IGNORED, REMOVING and anything newer
}

// TestConnection checks Scryer's URL and API key.
func (r *Router) TestConnection(ctx context.Context, req *pluginv1.TestConnectionRequest) (*pluginv1.TestConnectionResponse, error) {
	err := r.newClient(req.GetConnection()).Me(ctx)
	switch {
	case err == nil:
		return &pluginv1.TestConnectionResponse{Ok: true, Message: "Connected to Scryer."}, nil
	case errors.Is(err, scryer.ErrUnauthorized):
		return &pluginv1.TestConnectionResponse{Message: "Scryer refused the API key. Create a key in Scryer under your profile's API keys."}, nil
	default:
		return &pluginv1.TestConnectionResponse{Message: "Could not reach Scryer: " + err.Error()}, nil
	}
}

// ListConfigOptions offers Scryer's movie libraries for the anime movies
// dropdown. When Scryer cannot be read (bad key, no permission, unreachable)
// it offers nothing and anime movies keep going to the default library.
func (r *Router) ListConfigOptions(ctx context.Context, req *pluginv1.ListConfigOptionsRequest) (*pluginv1.ListConfigOptionsResponse, error) {
	resp := &pluginv1.ListConfigOptionsResponse{}
	libs, err := r.newClient(req.GetConnection()).Libraries(ctx, "MOVIE")
	if err != nil {
		return resp, nil
	}
	// Silo's form fills an empty dynamic choice with the first option, so the
	// first option is "none": the field must be able to stay unset.
	list := &pluginv1.ConfigOptionList{Options: []*pluginv1.ConfigOption{
		{Value: "", Label: "Scryer's default movie library"},
	}}
	for _, l := range libs {
		list.Options = append(list.Options, &pluginv1.ConfigOption{Value: l.ID, Label: l.Name})
	}
	resp.OptionsByField = map[string]*pluginv1.ConfigOptionList{configAnimeMoviesLibrary: list}
	return resp, nil
}

// Validate accepts every server: the only library setting is a dropdown of
// libraries that exist in Scryer, and one that has gone is handled when a
// request is fulfilled.
func (r *Router) Validate(context.Context, *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	return &pluginv1.ValidateResponse{}, nil
}
