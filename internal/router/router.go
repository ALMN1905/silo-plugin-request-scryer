// Package router implements Silo's request_router.v1 capability with Scryer:
// an approved request becomes a monitored title in Scryer, which then searches
// for it and hands the release to its download client.
package router

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/neilmc78/silo-plugin-request-scryer/internal/scryer"
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

	input := addTitleInput(req.GetRequest(), seriesMonitorType(conn))
	var gqlErr *scryer.GraphQLError
	if name := animeMovieLibrary(conn); name != "" && isAnimeMovie(req.GetRequest()) {
		id, err := client.LibraryID(ctx, "MOVIE", name)
		switch {
		case errors.As(err, &gqlErr), errors.Is(err, scryer.ErrLibraryNotFound):
			// Falling back to the default library would put the title in the
			// wrong place, so the target fails with the reason instead.
			for _, q := range qualities {
				resp.Targets = append(resp.Targets, target(q, "", statusFailed, "Scryer library for anime movies: "+err.Error()))
			}
			return resp, nil
		case err != nil:
			return nil, err
		}
		input.LibraryID = &id
	}

	titleID, err := client.AddTitle(ctx, input)
	switch {
	case errors.As(err, &gqlErr):
		for _, q := range qualities {
			resp.Targets = append(resp.Targets, target(q, "", statusFailed, "Scryer could not add it: "+gqlErr.Error()))
		}
		return resp, nil
	case err != nil:
		return nil, err
	}

	message := "Added to Scryer; searching for a release."
	if err := client.Search(ctx, titleID); err != nil {
		// The title is monitored, so Scryer's RSS sync still finds it.
		message = "Added to Scryer, but the search did not start; Scryer will pick it up from its RSS sync."
	}
	for _, q := range qualities {
		resp.Targets = append(resp.Targets, target(q, titleID, statusQueued, message))
	}
	return resp, nil
}

// Connection config keys for the anime movies library. The first is a
// dropdown filled from Scryer; the second is a free-text fallback for when the
// API key cannot read libraries. The dropdown wins when both are set.
const (
	configAnimeMoviesLibrary       = "anime_movies_library"
	configAnimeMoviesLibraryManual = "anime_movies_library_manual"
)

func isAnimeMovie(d *pluginv1.RequestDescriptor) bool {
	return d.GetMediaType() == "movie" && d.GetIsAnime()
}

// animeMovieLibrary returns the configured library name for anime movies, or ""
// when none is set (anime movies then go to Scryer's default movie library).
func animeMovieLibrary(conn *pluginv1.RouterConnection) string {
	cfg := conn.GetConfig().GetFields()
	for _, key := range []string{configAnimeMoviesLibrary, configAnimeMoviesLibraryManual} {
		if v := strings.TrimSpace(cfg[key].GetStringValue()); v != "" {
			return v
		}
	}
	return ""
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
		r.describe(st, title)
		resp.Statuses = append(resp.Statuses, st)
	}
	return resp, nil
}

func (r *Router) describe(st *pluginv1.TargetStatus, title *scryer.Title) {
	switch {
	case title == nil:
		st.Status, st.Message = statusFailed, "The title was removed from Scryer."
	case progressOf(title.Queue, r.now()) != nil:
		st.Status = statusDownloading
		st.Progress = progressOf(title.Queue, r.now())
	case len(title.MediaFiles) > 0:
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
// it offers nothing, and the admin falls back to the manual text field.
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
		list.Options = append(list.Options, &pluginv1.ConfigOption{Value: l.Name, Label: l.Name})
	}
	resp.OptionsByField = map[string]*pluginv1.ConfigOptionList{configAnimeMoviesLibrary: list}
	return resp, nil
}

// Validate checks that the anime movies library exists in Scryer. When
// Scryer cannot be read it accepts the name as typed: the check is only a
// convenience, and Fulfill reports a missing library on the request.
func (r *Router) Validate(ctx context.Context, req *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	resp := &pluginv1.ValidateResponse{}
	conn := req.GetConnection()
	name := animeMovieLibrary(conn)
	if name == "" {
		return resp, nil
	}
	fields := conn.GetConfig().GetFields()
	field := configAnimeMoviesLibrary
	if strings.TrimSpace(fields[configAnimeMoviesLibrary].GetStringValue()) == "" {
		field = configAnimeMoviesLibraryManual
	} else if strings.TrimSpace(fields[configAnimeMoviesLibraryManual].GetStringValue()) != "" {
		// The list wins, so a typed name would be ignored without notice.
		resp.FieldErrors = map[string]string{configAnimeMoviesLibraryManual: "Clear the library chosen in the list above to use a typed name."}
		return resp, nil
	}
	if _, err := r.newClient(conn).LibraryID(ctx, "MOVIE", name); errors.Is(err, scryer.ErrLibraryNotFound) {
		resp.FieldErrors = map[string]string{field: err.Error()}
	}
	return resp, nil
}
