package shelfarr_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adkulas/homelab/internal/shelfarr"
)

func TestReconcileAuthenticatesAndSubmitsOwnedSettings(t *testing.T) {
	const password = "FixtureHousehold1"
	var stored string
	var writes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /session/new":
			fmt.Fprint(w, `<meta name="csrf-token" content="session-form">`)
		case "POST /session":
			if r.FormValue("username") != "household" || r.FormValue("password") != password || r.Header.Get("X-CSRF-Token") != "session-form" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session_id", Value: "authenticated", Path: "/"})
			http.Redirect(w, r, "/", http.StatusFound)
		case "GET /admin/settings":
			if _, err := r.Cookie("session_id"); err != nil {
				http.Redirect(w, r, "/session/new", http.StatusFound)
				return
			}
			fmt.Fprint(w, `<meta name="csrf-token" content="settings-form">`)
		case "PATCH /admin/settings/bulk_update":
			if _, err := r.Cookie("session_id"); err != nil || r.Header.Get("X-CSRF-Token") != "settings-form" ||
				r.FormValue("settings[open_library_enabled]") != "true" || r.FormValue("settings[metadata_source]") != "auto" ||
				r.FormValue("settings[immediate_search_enabled]") != "true" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if token := r.FormValue("settings[hardcover_api_token]"); token != "" && stored != token {
				stored = token
				writes++
			}
			fmt.Fprint(w, `<turbo-stream>Settings updated successfully.</turbo-stream>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, token := range []string{"", "first-token", "first-token", "changed-token"} {
		client := shelfarr.New(server.URL, server.Client())
		if err := client.Reconcile(context.Background(), password, token); err != nil {
			t.Fatal(err)
		}
		if stored != token {
			t.Fatal("Shelfarr did not receive the declared token")
		}
	}
	if writes != 2 {
		t.Fatalf("settings changed %d times, want 2", writes)
	}
}
