package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSeasons(t *testing.T) {
	var gotAuth, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotKey = r.Header.Get("Authorization"), r.URL.Query().Get("api_key")
		if r.URL.Path != "/3/tv/123249" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"seasons":[{"season_number":0,"air_date":"2021-12-01","episode_count":2},{"season_number":1,"air_date":"2022-01-09","episode_count":24},{"season_number":2,"air_date":null,"episode_count":0}]}`))
	}))
	defer srv.Close()

	seasons, err := New(srv.URL, "eyJtoken").Seasons(context.Background(), "123249")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 2 || seasons[0].Number != 1 || seasons[0].Episodes != 24 || seasons[0].Start.Format("2006-01-02") != "2022-01-09" || !seasons[1].Start.IsZero() {
		t.Fatalf("seasons = %+v (specials must be left out, a missing date stays zero)", seasons)
	}
	if gotAuth != "Bearer eyJtoken" || gotKey != "" {
		t.Fatalf("token sent as auth=%q api_key=%q", gotAuth, gotKey)
	}
}

func TestSeasonsErrorsDoNotLeakTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	srv.Close() // unreachable
	_, err := New(srv.URL, "secret-key").Seasons(context.Background(), "1")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v", err)
	}
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer refused.Close()
	if _, err := New(refused.URL, "secret-key").Seasons(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsReadToken(t *testing.T) {
	for key, want := range map[string]bool{"eyJhbGciOi.payload.sig": true, " eyJabc ": true, "0123456789abcdef0123456789abcdef": false, "": false} {
		if got := IsReadToken(key); got != want {
			t.Errorf("IsReadToken(%q) = %v, want %v", key, got, want)
		}
	}
}
