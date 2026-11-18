package main

import (
	"testing"

	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

// The embedded manifest must load with the SDK, including the server form.
func TestManifestLoadsWithServerForm(t *testing.T) {
	m, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range m.GetCapabilities()[0].GetConfigSchema() {
		for _, f := range s.GetAdminForm().GetFields() {
			keys = append(keys, f.GetKey())
		}
	}
	if len(keys) != 4 || keys[0] != "anime_movies_library" || keys[1] != "series_monitor_type" || keys[2] != "monitor_series_specials" || keys[3] != "tag_titles" {
		t.Fatalf("form fields = %v", keys)
	}
}

// Silo offers more seasons of a series it already has only to a plugin that
// declares it takes seasons.
func TestManifestDeclaresSeasonSupport(t *testing.T) {
	m, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !m.GetCapabilities()[0].GetRequestRouter().GetSupportsSeasons() {
		t.Fatal("supports_seasons = false")
	}
}
