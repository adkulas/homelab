package acceptance_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDisposableStagingSeerrMovieRequestReachesPlayback(t *testing.T) {
	verifyDisposableStagingSeerrRequest(t,
		"MEDIA_STACK_LIVE_MOVIE_REQUEST",
		"MEDIA_STACK_LIVE_MOVIE_REQUEST_CONFIG",
		"legal-movie.yaml",
		"--legal-fixture",
		[]string{"VERIFY_MOVIE_REQUESTED", "VERIFY_MOVIE_ACQUIRED", "VERIFY_MOVIE_HARDLINKED", "VERIFY_MOVIE_PLAYBACK_READY"},
	)
}

func verifyDisposableStagingSeerrRequest(t *testing.T, enabledEnvironment, configEnvironment, fixtureName, fixtureFlag string, expectedCodes []string) {
	t.Helper()
	if os.Getenv(enabledEnvironment) != "1" {
		t.Skipf("set %s=1 to run the real disposable-Staging Seerr request acceptance test", enabledEnvironment)
	}
	configPath := os.Getenv(configEnvironment)
	if configPath == "" {
		t.Fatalf("set %s to an applied disposable Staging Environment configuration", configEnvironment)
	}
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(repositoryRoot(t), configPath)
	}
	fixturePath := filepath.Join(repositoryRoot(t), "stacks", "media", "fixtures", fixtureName)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "./cmd/media-stack", "verify",
		"--environment", "staging", "--config", configPath, "--suite", "promotion",
		fixtureFlag, fixturePath, "--output", "json")
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real disposable-Staging Seerr request verification failed: %v\n%s", err, output)
	}
	var report struct {
		Diagnostics []struct {
			Code   string `json:"code"`
			Status string `json:"status"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode real Seerr request verification: %v\n%s", err, output)
	}
	passed := map[string]bool{}
	for _, diagnostic := range report.Diagnostics {
		passed[diagnostic.Code] = diagnostic.Status == "pass"
	}
	for _, code := range expectedCodes {
		if !passed[code] {
			t.Errorf("real request-to-playback verification did not report %s: %s", code, output)
		}
	}
}
