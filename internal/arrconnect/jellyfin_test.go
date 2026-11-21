package arrconnect_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/sonarr"
)

func TestJellyfinConnectionReconcilesNativeTriggers(t *testing.T) {
	type connector interface {
		ReconcileJellyfinConnection(context.Context, string) error
	}
	for _, test := range []struct {
		name        string
		newClient   func(string, string, *http.Client) connector
		wantEnabled []string
	}{
		{"Radarr", func(url, key string, client *http.Client) connector { return radarr.New(url, key, client) }, []string{"onMovieDelete", "onMovieFileDelete"}},
		{"Sonarr", func(url, key string, client *http.Client) connector { return sonarr.New(url, key, client) }, []string{"onSeriesDelete", "onEpisodeFileDelete", "onImportComplete"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var configuration map[string]any
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "arr-api-key" {
					t.Error("request omitted Arr authentication")
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v3/notification":
					connections := []map[string]any{{"id": 7, "name": "Unmanaged connection", "implementation": "MediaBrowser"}}
					if configuration != nil {
						connections = append(connections, configuration)
					}
					_ = json.NewEncoder(w).Encode(connections)
				case "POST /api/v3/notification", "PUT /api/v3/notification/42":
					if err := json.NewDecoder(r.Body).Decode(&configuration); err != nil {
						t.Error(err)
					}
					configuration["id"] = 42
					writes++
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := test.newClient(server.URL, "arr-api-key", server.Client())
			for _, key := range []string{"initial-jellyfin-key", "rotated-jellyfin-key", "rotated-jellyfin-key"} {
				if err := client.ReconcileJellyfinConnection(context.Background(), key); err != nil {
					t.Fatal(err)
				}
			}
			if writes != 2 {
				t.Errorf("connector writes = %d, want one creation and one drift repair", writes)
			}
			for _, trigger := range append([]string{"onDownload", "onUpgrade", "onRename"}, test.wantEnabled...) {
				if configuration[trigger] != true {
					t.Errorf("%s trigger = %v, want enabled", trigger, configuration[trigger])
				}
			}
			if configuration["onGrab"] == true || configuration["onHealthIssue"] == true {
				t.Error("connector enabled a trigger unrelated to completed library changes")
			}
			fields := map[string]any{}
			for _, raw := range configuration["fields"].([]any) {
				field := raw.(map[string]any)
				fields[field["name"].(string)] = field["value"]
			}
			if fields["host"] != "jellyfin" || fields["port"] != float64(8096) || fields["updateLibrary"] != true || fields["notify"] != false || fields["apiKey"] != "rotated-jellyfin-key" {
				t.Error("connector did not use the intended Jellyfin endpoint, update mode and credential")
			}
		})
	}
}
