package profilarr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Connection struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Enabled int    `json:"enabled"`
}

// Observe uses only the supported, secret-stripping public API.
func Observe(ctx context.Context, baseURL, key string) ([]Connection, bool, error) {
	var connections []Connection
	status, err := readJSON(ctx, baseURL, "/api/v1/arr", key, &connections)
	if status == 0 {
		return nil, true, diagnostic("migration_failure", "Profilarr API is not ready")
	}
	if err != nil || (status == http.StatusOK && connections == nil) {
		return nil, false, diagnostic("verification_failure", "Profilarr returned an invalid connection list")
	}
	if status != http.StatusOK {
		return nil, status >= 500, diagnostic("verification_failure", "Profilarr documented connection API rejected verification")
	}
	return connections, false, nil
}

func Complete(connections []Connection) bool {
	if len(connections) != 2 {
		return false
	}
	want := map[string]string{"radarr": "http://radarr:7878", "sonarr": "http://sonarr:8989"}
	for _, connection := range connections {
		target, exists := want[connection.Type]
		if !exists || connection.Enabled != 1 || normalizeURL(connection.URL) != target {
			return false
		}
		delete(want, connection.Type)
	}
	return len(want) == 0
}
func normalizeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return parsed.Scheme + "://" + host + strings.TrimRight(parsed.EscapedPath(), "/")
}

// Synchronized requires observable success for every selected policy section.
// Existing matching Arr resources alone cannot prove the bootstrap jobs ran.
func Synchronized(ctx context.Context, baseURL, key string) error {
	ready, err := Ready(ctx, baseURL, key)
	if err != nil {
		return err
	}
	if !ready {
		return diagnostic("verification_failure", "Profilarr caches are not healthy")
	}
	type section struct {
		Status       string
		LastSyncedAt *string
	}
	var status struct {
		Arrs []struct {
			Type string
			Sync struct {
				QualityProfiles section
				DelayProfiles   section
				MediaManagement section
			}
		}
	}
	code, err := readJSON(ctx, baseURL, "/api/v1/status", key, &status)
	if err != nil || code != http.StatusOK || len(status.Arrs) != 2 {
		return diagnostic("verification_failure", "Profilarr synchronization status is incomplete")
	}
	seen := map[string]bool{}
	for _, arr := range status.Arrs {
		if (arr.Type != "radarr" && arr.Type != "sonarr") || seen[arr.Type] {
			return diagnostic("verification_failure", "Profilarr synchronization topology is unexpected")
		}
		seen[arr.Type] = true
		for _, section := range []section{arr.Sync.QualityProfiles, arr.Sync.DelayProfiles, arr.Sync.MediaManagement} {
			if section.Status == "failed" {
				return diagnostic("synchronization_failure", "Profilarr reported a failed policy synchronization")
			}
			if section.Status != "idle" || section.LastSyncedAt == nil || *section.LastSyncedAt == "" {
				return diagnostic("verification_failure", "Profilarr policy synchronization has not completed successfully")
			}
		}
	}
	return nil
}

func Ready(ctx context.Context, baseURL, key string) (bool, error) {
	var health struct{ Status string }
	code, err := readJSON(ctx, baseURL, "/api/v1/health", key, &health)
	if code == 0 || code >= 500 {
		return false, nil
	}
	if err != nil || code != http.StatusOK {
		return false, diagnostic("migration_failure", "Profilarr readiness API could not be verified")
	}
	return health.Status == "healthy", nil
}

// VerifyZeroDelay observes the Arr default delay profile; Profilarr remains its writer.
func VerifyZeroDelay(ctx context.Context, baseURL, key string) error {
	var profiles []struct {
		ID                        int
		TorrentDelay, UsenetDelay *int
	}
	code, err := readJSON(ctx, baseURL, "/api/v3/delayprofile", key, &profiles)
	if err != nil || code != http.StatusOK {
		return diagnostic("verification_failure", "Arr zero-delay policy could not be verified")
	}
	for _, profile := range profiles {
		if profile.ID == 1 && profile.TorrentDelay != nil && profile.UsenetDelay != nil && *profile.TorrentDelay == 0 && *profile.UsenetDelay == 0 {
			return nil
		}
	}
	return diagnostic("verification_failure", "Arr default zero-delay policy differs")
}

// readJSON limits every response and never propagates upstream errors or bodies,
// which can reflect credentials. Status zero means no HTTP response was received;
// endpoint-specific readiness/retry decisions remain with the callers.
func readJSON(ctx context.Context, baseURL, path, key string, target any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return -1, errors.New("invalid API address")
	}
	request.Header.Set("X-Api-Key", key)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return 0, errors.New("API not reachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if decoder.Decode(target) != nil {
		return response.StatusCode, errors.New("invalid API document")
	}
	return response.StatusCode, nil
}
