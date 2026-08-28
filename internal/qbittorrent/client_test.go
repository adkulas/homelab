package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestLoginAcceptsPinnedNoContentContractOnlyAfterProtectedObservation(t *testing.T) {
	protectedCalls := 0
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/auth/login":
			writer.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			protectedCalls++
			_, _ = writer.Write([]byte("v5.1.2"))
		default:
			http.NotFound(writer, request)
		}
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	if err := client.Login(context.Background(), "household", "declared-password"); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if protectedCalls != 1 {
		t.Fatalf("protected observations = %d, want 1", protectedCalls)
	}
}

func TestLoginRetainsPinnedNoContentCookieForProtectedObservation(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(writer, &http.Cookie{Name: "SID", Value: "pinned-session", Path: "/"})
			writer.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			cookie, err := request.Cookie("SID")
			if err != nil || cookie.Value != "pinned-session" {
				http.Error(writer, "Forbidden", http.StatusForbidden)
				return
			}
			_, _ = writer.Write([]byte("v5.2.3"))
		default:
			http.NotFound(writer, request)
		}
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	if err := client.Login(context.Background(), "household", "declared-password"); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestLoginRejectsNoContentWhenProtectedObservationFails(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/auth/login" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(writer, "Forbidden", http.StatusForbidden)
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	err := client.Login(context.Background(), "household", "declared-password")
	if err == nil || !strings.Contains(err.Error(), "protected API observation") {
		t.Fatalf("Login() error = %v, want protected observation failure", err)
	}
}

func TestReconcileWebUICredentialsUsesSupportedPreferencesAPI(t *testing.T) {
	const password = "declared-password-must-not-leak"
	var update url.Values
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v2/app/setPreferences" {
			http.NotFound(writer, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		update, _ = url.ParseQuery(string(body))
	})
	client := New("http://qbittorrent:18080", &http.Client{Transport: handlerTransport{handler: handler}})
	if err := client.ReconcileWebUICredentials(context.Background(), Credentials{Username: "household", Password: password}); err != nil {
		t.Fatalf("ReconcileWebUICredentials() error = %v", err)
	}
	var preferences map[string]string
	if err := json.Unmarshal([]byte(update.Get("json")), &preferences); err != nil {
		t.Fatal(err)
	}
	if preferences["web_ui_username"] != "household" || preferences["web_ui_password"] != password {
		t.Fatalf("credential preferences = %#v", preferences)
	}
}

func TestReconcileAcquisitionPolicyConvergesContractFixture(t *testing.T) {
	preferences := readFixture(t, "preferences.json")
	categories := readFixture(t, "categories.json")
	var mutations []string

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/auth/login" {
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("username") != "admin" || values.Get("password") != "fixture-password" || request.Header.Get("Referer") != "http://qbittorrent:8080" {
				http.Error(writer, "Fails.", http.StatusForbidden)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "SID", Value: "fixture-session", Path: "/"})
			_, _ = writer.Write([]byte("Ok."))
			return
		}
		cookie, err := request.Cookie("SID")
		if err != nil || cookie.Value != "fixture-session" {
			http.Error(writer, "Forbidden", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/api/v2/app/preferences":
			_, _ = writer.Write(preferences)
		case "/api/v2/torrents/categories":
			_, _ = writer.Write(categories)
		case "/api/v2/app/setPreferences":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			var update map[string]any
			if err := json.Unmarshal([]byte(values.Get("json")), &update); err != nil {
				t.Fatalf("decode preference update: %v", err)
			}
			preferences, _ = json.Marshal(update)
			mutations = append(mutations, request.URL.Path)
		case "/api/v2/torrents/createCategory":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			var current map[string]Category
			_ = json.Unmarshal(categories, &current)
			current[values.Get("category")] = Category{Name: values.Get("category"), SavePath: values.Get("savePath")}
			categories, _ = json.Marshal(current)
			mutations = append(mutations, request.URL.Path+":"+values.Get("category"))
		default:
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
		}
	})

	client := New("http://qbittorrent:8080", &http.Client{Transport: handlerTransport{handler: handler}})
	if err := client.Login(context.Background(), "admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	changed, err := client.ReconcileAcquisitionPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first reconciliation reported no changes")
	}
	sort.Strings(mutations)
	wantMutations := []string{
		"/api/v2/app/setPreferences",
		"/api/v2/torrents/createCategory:movies",
		"/api/v2/torrents/createCategory:series",
	}
	if !reflect.DeepEqual(mutations, wantMutations) {
		t.Fatalf("mutations = %v, want %v", mutations, wantMutations)
	}

	mutations = nil
	changed, err = client.ReconcileAcquisitionPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(mutations) != 0 {
		t.Fatalf("repeated reconciliation changed state: changed=%v mutations=%v", changed, mutations)
	}

	var gotPreferences map[string]any
	if err := json.Unmarshal(preferences, &gotPreferences); err != nil {
		t.Fatal(err)
	}
	wantPreferences := map[string]any{
		"save_path":                     "/data/torrents",
		"auto_tmm_enabled":              true,
		"torrent_changed_tmm_enabled":   true,
		"save_path_changed_tmm_enabled": true,
		"category_changed_tmm_enabled":  true,
		"max_ratio_enabled":             true,
		"max_ratio":                     float64(1),
		"max_seeding_time_enabled":      true,
		"max_seeding_time":              float64(7 * 24 * 60),
		"max_ratio_act":                 float64(0),
	}
	if !reflect.DeepEqual(gotPreferences, wantPreferences) {
		t.Errorf("preferences = %#v, want %#v", gotPreferences, wantPreferences)
	}
}

type handlerTransport struct {
	handler http.Handler
}

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := &responseWriter{header: make(http.Header), status: http.StatusOK}
	transport.handler.ServeHTTP(recorder, request)
	return &http.Response{StatusCode: recorder.status, Header: recorder.header, Body: io.NopCloser(&recorder.body), Request: request}, nil
}

type responseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (writer *responseWriter) Header() http.Header            { return writer.header }
func (writer *responseWriter) Write(body []byte) (int, error) { return writer.body.Write(body) }
func (writer *responseWriter) WriteHeader(status int)         { writer.status = status }

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
