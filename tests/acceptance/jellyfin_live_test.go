package acceptance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
)

func TestDisposableJellyfinServesImportedMovieReadOnly(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_JELLYFIN") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_JELLYFIN=1 to run the pinned disposable Jellyfin acceptance test")
	}
	versions, err := config.LoadVersions(filepath.Join(repositoryRoot(t), "stacks", "media", "versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	image := versions.Images["jellyfin"]
	temporary := t.TempDir()
	configDirectory := filepath.Join(temporary, "config")
	cacheDirectory := filepath.Join(temporary, "cache")
	movieDirectory := filepath.Join(temporary, "media", "movies")
	for _, directory := range []string{configDirectory, cacheDirectory, movieDirectory} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	moviePath := filepath.Join(movieDirectory, "legal-fixture.mp4")
	generate := exec.Command("docker", "run", "--rm", "--entrypoint", "/usr/lib/jellyfin-ffmpeg/ffmpeg",
		"--volume", movieDirectory+":/fixture", image,
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "/fixture/legal-fixture.mp4")
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate legal movie fixture: %v\n%s", err, output)
	}
	if _, err := os.Stat(moviePath); err != nil {
		t.Fatal(err)
	}

	containerName := fmt.Sprintf("media-stack-jellyfin-acceptance-%d", os.Getpid())
	start := exec.Command("docker", "run", "--detach", "--rm", "--name", containerName,
		"--publish", "127.0.0.1::8096",
		"--volume", configDirectory+":/config",
		"--volume", cacheDirectory+":/cache",
		"--volume", filepath.Join(temporary, "media")+":/data/media:ro",
		image)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start disposable Jellyfin: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", containerName).Run() })
	portOutput, err := exec.Command("docker", "port", containerName, "8096/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + strings.TrimSpace(string(portOutput))
	if parsed, parseErr := url.Parse(address); parseErr != nil || parsed.Port() == "" {
		t.Fatalf("Jellyfin address = %q: %v", address, parseErr)
	}
	client := jellyfin.New(address, &http.Client{Timeout: 10 * time.Second})
	credentials := jellyfin.Credentials{Username: "household", Password: "fixture-jellyfin-password"}
	deadline := time.Now().Add(2 * time.Minute)
	pollJellyfin(t, deadline, "reconcile disposable Jellyfin", func() (bool, error) {
		return true, client.ReconcileMovieLibrary(context.Background(), credentials)
	})
	pollJellyfin(t, deadline, "discover the legal movie with direct-play readiness", func() (bool, error) {
		return client.MoviePlaybackReady(context.Background(), credentials, "/data/media/movies/legal-fixture.mp4")
	})
	deletionDisabled, err := client.DestructiveDeletionDisabled(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	if !deletionDisabled {
		t.Fatal("disposable Jellyfin retained destructive deletion permission")
	}
	if output, err := exec.Command("docker", "exec", containerName, "sh", "-c", "touch /data/media/write-must-fail").CombinedOutput(); err == nil {
		t.Fatalf("Jellyfin wrote through its read-only Movie and Series Libraries mount: %s", output)
	}
}

func pollJellyfin(t *testing.T, deadline time.Time, behavior string, observe func() (bool, error)) {
	t.Helper()
	for {
		complete, err := observe()
		if err == nil && complete {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("disposable Jellyfin did not %s: %v", behavior, err)
		}
		time.Sleep(time.Second)
	}
}
