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

func TestDisposableJellyfinRestoreDrillRotatesCredentialsAndServesRecoveredMediaReadOnly(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_JELLYFIN") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_JELLYFIN=1 to run the pinned disposable Jellyfin Restore Drill acceptance test")
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
	seriesDirectory := filepath.Join(temporary, "media", "series", "The Lucy Show", "Season 01")
	for _, directory := range []string{configDirectory, cacheDirectory, movieDirectory, seriesDirectory} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	moviePath := filepath.Join(movieDirectory, "legal-fixture.mp4")
	episodePath := filepath.Join(seriesDirectory, "The Lucy Show - S01E01.mp4")
	for _, fixture := range []struct {
		directory   string
		path        string
		description string
	}{{movieDirectory, moviePath, "movie"}, {seriesDirectory, episodePath, "series episode"}} {
		generate := exec.Command("docker", "run", "--rm", "--entrypoint", "/usr/lib/jellyfin-ffmpeg/ffmpeg",
			"--volume", fixture.directory+":/fixture", image,
			"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=1",
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "/fixture/"+filepath.Base(fixture.path))
		if output, err := generate.CombinedOutput(); err != nil {
			t.Fatalf("generate legal %s fixture: %v\n%s", fixture.description, err, output)
		}
	}

	containerName := fmt.Sprintf("media-stack-jellyfin-acceptance-%d", os.Getpid())
	start := exec.Command("docker", "run", "--detach", "--rm", "--name", containerName,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
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
		return true, client.ReconcileLibraries(context.Background(), credentials)
	})
	drillCredentials := jellyfin.Credentials{Username: "restore-drill-admin", Password: "restore-drill-password"}
	if err := client.PrepareRestoreDrill(context.Background(), credentials, drillCredentials); err != nil {
		t.Fatalf("rotate Restore Drill credentials and verify recovered libraries: %v", err)
	}
	credentials = drillCredentials
	pollJellyfin(t, deadline, "discover the legal movie with direct-play readiness", func() (bool, error) {
		return client.MoviePlaybackReady(context.Background(), credentials, "/data/media/movies/legal-fixture.mp4")
	})
	pollJellyfin(t, deadline, "discover the legal series episode with direct-play readiness", func() (bool, error) {
		return client.EpisodePlaybackReady(context.Background(), credentials, "/data/media/series/The Lucy Show/Season 01/The Lucy Show - S01E01.mp4")
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
