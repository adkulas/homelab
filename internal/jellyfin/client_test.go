package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReconcileMovieLibraryBootstrapsAuthenticationAndDisablesDeletion(t *testing.T) {
	startupComplete := false
	libraries := []map[string]any{}
	policy := map[string]any{
		"IsAdministrator":                  true,
		"EnableMediaPlayback":              true,
		"EnableContentDeletion":            true,
		"EnableContentDeletionFromFolders": []any{"old-library"},
	}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /System/Info/Public":
			_ = json.NewEncoder(writer).Encode(map[string]any{"StartupWizardCompleted": startupComplete})
		case "POST /Startup/Configuration", "POST /Startup/RemoteAccess", "POST /Startup/Complete":
			writes++
			if request.URL.Path == "/Startup/Complete" {
				startupComplete = true
			}
			writer.WriteHeader(http.StatusNoContent)
		case "POST /Startup/User":
			writes++
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["Name"] != "household" || body["Password"] != "fixture-jellyfin-password" {
				t.Errorf("startup user = %#v", body)
			}
			writer.WriteHeader(http.StatusNoContent)
		case "POST /Users/AuthenticateByName":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["Username"] != "household" || body["Pw"] != "fixture-jellyfin-password" {
				t.Errorf("authentication = %#v", body)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"AccessToken": "fixture-access-token",
				"User":        map[string]any{"Id": "user-1", "Policy": policy},
			})
		case "GET /Library/VirtualFolders":
			requireToken(t, request)
			_ = json.NewEncoder(writer).Encode(libraries)
		case "POST /Library/VirtualFolders":
			requireToken(t, request)
			writes++
			if request.URL.Query().Get("name") != "Movie Library" || request.URL.Query().Get("collectionType") != "movies" || request.URL.Query()["paths"][0] != "/data/media/movies" {
				t.Errorf("library query = %s", request.URL.RawQuery)
			}
			libraries = append(libraries, map[string]any{"Name": "Movie Library", "CollectionType": "movies", "Locations": []string{"/data/media/movies"}})
			writer.WriteHeader(http.StatusNoContent)
		case "POST /Users/user-1/Policy":
			requireToken(t, request)
			writes++
			_ = json.NewDecoder(request.Body).Decode(&policy)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := New(server.URL, server.Client())
	if err := client.ReconcileMovieLibrary(context.Background(), Credentials{Username: "household", Password: "fixture-jellyfin-password"}); err != nil {
		t.Fatalf("reconcile Movie Library: %v", err)
	}
	if policy["EnableContentDeletion"] != false {
		t.Fatalf("content deletion = %#v, want false", policy["EnableContentDeletion"])
	}
	if folders, ok := policy["EnableContentDeletionFromFolders"].([]any); !ok || len(folders) != 0 {
		t.Fatalf("folder deletion policy = %#v, want empty", policy["EnableContentDeletionFromFolders"])
	}
	if policy["IsAdministrator"] != true || policy["EnableMediaPlayback"] != true {
		t.Fatalf("unrelated user policy was not preserved: %#v", policy)
	}
	writesAfterFirstRun := writes
	if err := client.ReconcileMovieLibrary(context.Background(), Credentials{Username: "household", Password: "fixture-jellyfin-password"}); err != nil {
		t.Fatalf("repeat reconciliation: %v", err)
	}
	if writes != writesAfterFirstRun {
		t.Fatalf("repeat reconciliation made %d writes, want none", writes-writesAfterFirstRun)
	}
}

func TestMovieIsDiscoverableAndPlaybackReadyForAuthenticatedUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "POST /Users/AuthenticateByName":
			_ = json.NewEncoder(writer).Encode(map[string]any{"AccessToken": "fixture-access-token", "User": map[string]any{"Id": "user-1", "Policy": map[string]any{}}})
		case "GET /Users/user-1/Items":
			requireToken(t, request)
			_ = json.NewEncoder(writer).Encode(map[string]any{"Items": []any{map[string]any{"Id": "movie-1", "Name": "Legal Movie", "Path": "/data/media/movies/legal/movie.mp4"}}})
		case "GET /Items/movie-1/PlaybackInfo":
			requireToken(t, request)
			_ = json.NewEncoder(writer).Encode(map[string]any{"MediaSources": []any{map[string]any{"Path": "/data/media/movies/legal/movie.mp4", "SupportsDirectPlay": true}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	ready, err := New(server.URL, server.Client()).MoviePlaybackReady(context.Background(), Credentials{Username: "household", Password: "fixture-jellyfin-password"}, "/data/media/movies/legal/movie.mp4")
	if err != nil {
		t.Fatalf("verify movie playback: %v", err)
	}
	if !ready {
		t.Fatal("authenticated user could not discover and directly play the imported movie")
	}
}

func requireToken(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("X-Emby-Token") != "fixture-access-token" {
		t.Errorf("token = %q", request.Header.Get("X-Emby-Token"))
	}
}
