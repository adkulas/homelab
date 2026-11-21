package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReconcileRefreshPolicyRepairsBothMonitorsAndReplacesScanTriggers(t *testing.T) {
	libraries := []map[string]any{
		{"Name": "Movie Library", "ItemId": "movies", "LibraryOptions": map[string]any{"EnableRealtimeMonitor": false, "MetadataCountryCode": "CA"}},
		{"Name": "Series Library", "ItemId": "series", "LibraryOptions": map[string]any{"EnableRealtimeMonitor": false, "MetadataCountryCode": "CA"}},
		{"Name": "Other Library", "ItemId": "other", "LibraryOptions": map[string]any{"EnableRealtimeMonitor": false}},
	}
	triggers := json.RawMessage(`[{"Type":"StartupTrigger"},{"Type":"IntervalTrigger"}]`)
	writes, activations := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /Users/AuthenticateByName":
			_, _ = w.Write([]byte(`{"AccessToken":"fixture-access-token","User":{"Id":"administrator"}}`))
		case "GET /Library/VirtualFolders":
			requireToken(t, r)
			_ = json.NewEncoder(w).Encode(libraries)
		case "POST /Library/VirtualFolders/LibraryOptions":
			requireToken(t, r)
			var body struct {
				ID             string         `json:"Id"`
				LibraryOptions map[string]any `json:"LibraryOptions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, library := range libraries[:2] {
				if library["ItemId"] == body.ID {
					library["LibraryOptions"] = body.LibraryOptions
				}
			}
			writes++
			w.WriteHeader(http.StatusNoContent)
		case "GET /ScheduledTasks":
			requireToken(t, r)
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"Key": "RefreshLibrary", "Id": "scan", "Triggers": triggers}})
		case "POST /ScheduledTasks/scan/Triggers":
			requireToken(t, r)
			if err := json.NewDecoder(r.Body).Decode(&triggers); err != nil {
				t.Error(err)
			}
			writes++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Jellyfin request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.URL, server.Client())
	prepareMonitorChange := func() error { activations++; return nil }
	for range 2 {
		if err := client.ReconcileRefreshPolicy(context.Background(), Credentials{Username: "admin", Password: "fixture"}, 0, prepareMonitorChange); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 3 || activations != 1 {
		t.Errorf("writes = %d, activation preparations = %d, want 3 writes and 1 preparation across both applies", writes, activations)
	}
	if got := string(triggers); got != `[{"Type":"DailyTrigger","TimeOfDayTicks":0}]` {
		t.Errorf("scan triggers = %s", got)
	}
	for _, library := range libraries[:2] {
		options := library["LibraryOptions"].(map[string]any)
		if options["EnableRealtimeMonitor"] != true || options["MetadataCountryCode"] != "CA" {
			t.Errorf("%s options = %v", library["Name"], options)
		}
	}
	if libraries[2]["LibraryOptions"].(map[string]any)["EnableRealtimeMonitor"] != false {
		t.Error("unrelated library changed")
	}
}

func TestRefreshAPIKeyRecoversInterruptedPersistenceAndIsolatesRestoredProjects(t *testing.T) {
	keys := map[string]string{
		"Media Stack Library Refresh (media-production)": "production-connector-key",
		"Independent client":                             "unmanaged-key",
	}
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /Users/AuthenticateByName":
			_, _ = w.Write([]byte(`{"AccessToken":"fixture-access-token","User":{"Id":"administrator"}}`))
		case "GET /Auth/Keys":
			requireToken(t, r)
			var result []map[string]string
			for app, key := range keys {
				result = append(result, map[string]string{"AppName": app, "AccessToken": key})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": result})
		case "POST /Auth/Keys":
			requireToken(t, r)
			created++
			keys[r.URL.Query().Get("app")] = "staging-connector-key"
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /Auth/Keys/production-connector-key":
			requireToken(t, r)
			delete(keys, "Media Stack Library Refresh (media-production)")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected Jellyfin request")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.URL, server.Client())
	ctx := context.Background()
	credentials := Credentials{Username: "admin", Password: "fixture"}
	for _, stored := range []string{"production-connector-key", "", "staging-connector-key"} {
		key, err := client.ReconcileRefreshAPIKey(ctx, credentials, "media-staging", stored)
		if err != nil || key != "staging-connector-key" {
			t.Fatalf("did not recover the Staging refresh credential: %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("created %d keys, want one across retries", created)
	}
	if err := client.RemoveForeignRefreshAPIKeys(ctx, credentials, "media-staging"); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys["Independent client"] != "unmanaged-key" || keys["Media Stack Library Refresh (media-staging)"] != "staging-connector-key" {
		t.Fatal("Restore Drill revocation did not preserve local and unmanaged credentials")
	}
}
