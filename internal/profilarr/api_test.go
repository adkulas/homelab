package profilarr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/adkulas/homelab/internal/profilarr"
)

func TestObserveRejectsAmbiguousConnectionDocumentsWithoutExposingSecrets(t *testing.T) {
	for index, body := range []string{"null", `{"api_key":"private-key"}`, `[{"enabled":"invalid","api_key":"private-key"}]`} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			_, _, err := profilarr.Observe(context.Background(), server.URL, "private-key")
			if err == nil {
				t.Fatal("ambiguous document was accepted as fresh")
			}
			if strings.Contains(err.Error(), "private-key") {
				t.Fatal("diagnostic exposed response content")
			}
		})
	}
}
func TestCompleteRequiresExactlyTheNormalizedDeclaredTargets(t *testing.T) {
	connections := []profilarr.Connection{{Type: "radarr", URL: " HTTP://RADARR:7878/?ignored=1#fragment ", Enabled: 1}, {Type: "sonarr", URL: "http://sonarr:8989///", Enabled: 1}}
	if !profilarr.Complete(connections) {
		t.Fatal("Profilarr-equivalent URLs were rejected")
	}
	connections = append(connections, connections[0])
	if profilarr.Complete(connections) {
		t.Fatal("duplicate connection was accepted")
	}
	connections = connections[:2]
	connections[0].Enabled = 0
	if profilarr.Complete(connections) {
		t.Fatal("disabled connection was accepted")
	}
}

func TestSynchronizationStatusUsesBoundedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
			return
		}
		section := `{"status":"idle","lastSyncedAt":"2026-11-03T00:00:00Z"}`
		sync := `{"qualityProfiles":` + section + `,"delayProfiles":` + section + `,"mediaManagement":` + section + `}`
		_, _ = w.Write([]byte(`{"padding":"` + strings.Repeat("x", 2<<20) + `","arrs":[{"type":"radarr","sync":` + sync + `},{"type":"sonarr","sync":` + sync + `}]}`))
	}))
	defer server.Close()
	if err := profilarr.Synchronized(context.Background(), server.URL, "private-key"); err == nil {
		t.Fatal("oversized synchronization status was accepted")
	}
}
