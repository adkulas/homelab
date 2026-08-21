package seerr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReconcileAuthenticationKeepsJellyfinAndEmergencyLocalSignInAvailable(t *testing.T) {
	initialized := false
	jellyfinConfigured := false
	localPassword := ""
	main := mainSettings{LocalLogin: false, MediaServerLogin: false, NewJellyfinLogin: false, DefaultPermissions: 0}
	writes := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/settings/public":
			mediaServerType := 4
			if jellyfinConfigured {
				mediaServerType = 2
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"initialized": initialized, "mediaServerType": mediaServerType})
		case "POST /api/v1/auth/jellyfin":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["username"] != "household" || body["password"] != "fixture-jellyfin-password" {
				t.Errorf("Jellyfin authentication = %#v", body)
			}
			if !jellyfinConfigured && (body["hostname"] != "jellyfin" || body["port"] != float64(8096) || body["serverType"] != float64(2)) {
				t.Errorf("Jellyfin bootstrap = %#v", body)
			}
			jellyfinConfigured = true
			http.SetCookie(writer, &http.Cookie{Name: "connect.sid", Value: "owner-session", Path: "/"})
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": 1, "email": "household", "permissions": 2})
		case "POST /api/v1/auth/local":
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["email"] != "household" || body["password"] != localPassword || localPassword == "" {
				http.Error(writer, "Access denied", http.StatusForbidden)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "connect.sid", Value: "owner-session", Path: "/"})
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": 1, "email": "household", "permissions": 2})
		case "GET /api/v1/auth/me":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": 1, "email": "household", "permissions": administratorPermission})
		case "GET /api/v1/settings/main":
			requireOwnerSession(t, request)
			_ = json.NewEncoder(writer).Encode(main)
		case "POST /api/v1/settings/main":
			requireOwnerSession(t, request)
			writes++
			_ = json.NewDecoder(request.Body).Decode(&main)
			_ = json.NewEncoder(writer).Encode(main)
		case "POST /api/v1/user/1/settings/password":
			requireOwnerSession(t, request)
			writes++
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			localPassword = body["newPassword"]
			writer.WriteHeader(http.StatusNoContent)
		case "POST /api/v1/settings/initialize":
			requireOwnerSession(t, request)
			writes++
			initialized = true
			_ = json.NewEncoder(writer).Encode(map[string]any{"initialized": true})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	credentials := Credentials{Username: "household", Password: "fixture-jellyfin-password"}
	if err := client.ReconcileAuthentication(context.Background(), credentials); err != nil {
		t.Fatalf("reconcile Seerr authentication: %v", err)
	}
	if !initialized || !main.LocalLogin || !main.MediaServerLogin || !main.NewJellyfinLogin || main.DefaultPermissions != requestPermission {
		t.Fatalf("Seerr main settings = %#v, initialized=%t", main, initialized)
	}
	if localPassword != credentials.Password {
		t.Fatal("emergency local administrator password was not reconciled")
	}

	writesAfterFirstRun := writes
	if err := client.ReconcileAuthentication(context.Background(), credentials); err != nil {
		t.Fatalf("repeat reconciliation: %v", err)
	}
	if writes != writesAfterFirstRun {
		t.Fatalf("repeat reconciliation made %d writes, want none", writes-writesAfterFirstRun)
	}
}

func TestVerifyLocalAdministratorRejectsRequestOnlyUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "POST /api/v1/auth/local":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":          7,
				"email":       "household",
				"permissions": requestPermission,
			})
		case "GET /api/v1/auth/me":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":          7,
				"email":       "household",
				"permissions": requestPermission,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.VerifyLocalAdministrator(context.Background(), Credentials{
		Username: "household",
		Password: "fixture-jellyfin-password",
	})
	if err == nil || !strings.Contains(err.Error(), "lacks administrator permission") {
		t.Fatalf("VerifyLocalAdministrator error = %v, want missing administrator permission", err)
	}
}
func requireOwnerSession(t *testing.T, request *http.Request) {
	t.Helper()
	cookie, err := request.Cookie("connect.sid")
	if err != nil || cookie.Value != "owner-session" {
		t.Errorf("owner session missing from %s %s", request.Method, request.URL.Path)
	}
}
