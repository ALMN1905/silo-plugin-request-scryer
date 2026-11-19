// Package tmdb reads a series' seasons from The Movie Database.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/neilmc78/silo-plugin-request-scryer/internal/seasonmap"
)

// Errors a caller tells apart: a refused key or an unknown series will not
// change by asking again, an unreachable TMDB may.
var (
	ErrKeyRefused  = errors.New("TMDB refused the API key")
	ErrNotFound    = errors.New("TMDB does not know the series")
	ErrUnreachable = errors.New("TMDB unreachable")
)

// DefaultBase is TMDB's API.
const DefaultBase = "https://api.themoviedb.org"

// Client calls TMDB with an API Read Access Token, which gives read access only.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Key     string
}

// New returns a client; an empty base means TMDB's own.
func New(base, key string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, BaseURL: strings.TrimRight(base, "/"), Key: strings.TrimSpace(key)}
}

// Seasons lists the regular seasons (not specials) of a series.
func (c *Client) Seasons(ctx context.Context, tmdbID string) ([]seasonmap.Season, error) {
	u := c.BaseURL + "/3/tv/" + url.PathEscape(tmdbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// The URL carries the key for a v3 key: report without it.
		return nil, ErrUnreachable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrKeyRefused
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, tmdbID)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("TMDB answered HTTP %d", resp.StatusCode)
	}
	var out struct {
		Seasons []struct {
			Number   int    `json:"season_number"`
			AirDate  string `json:"air_date"`
			Episodes int    `json:"episode_count"`
		} `json:"seasons"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode TMDB response: %w", err)
	}
	var seasons []seasonmap.Season
	for _, s := range out.Seasons {
		if s.Number <= 0 {
			continue
		}
		start, _ := time.Parse(time.DateOnly, s.AirDate) // zero when TMDB has no date yet
		seasons = append(seasons, seasonmap.Season{Number: s.Number, Start: start, Episodes: s.Episodes})
	}
	return seasons, nil
}

// IsReadToken reports whether key looks like an API Read Access Token. TMDB issues
// them as JWTs, which start with "eyJ"; the API key, which is not read-only, does not.
func IsReadToken(key string) bool { return strings.HasPrefix(strings.TrimSpace(key), "eyJ") }
