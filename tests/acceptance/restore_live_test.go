package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
)

func TestDisposableStagingRestoreDrillRestoresProductionBackupSafely(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_RESTORE_DRILL") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_RESTORE_DRILL=1 to run the full disposable Restore Drill acceptance test")
	}
	repository := repositoryRoot(t)
	versions, err := config.LoadVersions(filepath.Join(repository, "stacks", "media", "versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	jellyfinImage := versions.Images["jellyfin"]
	temporary := t.TempDir()
	productionProject := fmt.Sprintf("media-production-drill-%d", os.Getpid())
	stagingProject := fmt.Sprintf("media-staging-drill-%d", os.Getpid())
	for _, project := range []string{productionProject, stagingProject} {
		projectToClean := project
		t.Cleanup(func() {
			output, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+projectToClean).Output()
			for _, container := range strings.Fields(string(output)) {
				_ = exec.Command("docker", "rm", "--force", container).Run()
			}
		})
	}
	configPath := liveRestoreDrillConfig(t, repository, temporary, productionProject, stagingProject)
	productionData := filepath.Join(temporary, "data", "production")
	stagingData := filepath.Join(temporary, "data", "staging")
	for _, root := range []string{productionData, stagingData} {
		for _, relative := range []string{"media/movies", "media/series/The Lucy Show/Season 01", "torrents/movies", "torrents/series"} {
			if err := os.MkdirAll(filepath.Join(root, relative), 0o750); err != nil {
				t.Fatal(err)
			}
		}
	}
	liveCreateMediaFixture(t, jellyfinImage, filepath.Join(productionData, "media", "movies"), "recovered-movie.mp4")
	liveCreateMediaFixture(t, jellyfinImage, filepath.Join(productionData, "media", "series", "The Lucy Show", "Season 01"), "The Lucy Show - S01E01.mp4")
	copyFile(t, filepath.Join(productionData, "media", "movies", "recovered-movie.mp4"), filepath.Join(stagingData, "media", "movies", "recovered-movie.mp4"), 0o600)
	copyFile(t, filepath.Join(productionData, "media", "series", "The Lucy Show", "Season 01", "The Lucy Show - S01E01.mp4"), filepath.Join(stagingData, "media", "series", "The Lucy Show", "Season 01", "The Lucy Show - S01E01.mp4"), 0o600)

	for _, project := range []string{productionProject, stagingProject} {
		for _, service := range backupFixtureServiceNames {
			volume := project + "_" + service + "-config"
			liveDocker(t, "volume", "create", volume)
			liveWriteVolumeFile(t, jellyfinImage, volume, project+"-"+service+"\n")
			t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "--force", volume).Run() })
		}
	}

	productionJellyfinVolume := productionProject + "_jellyfin-config"
	liveDocker(t, "run", "--rm", "--user", "0:0", "--volume", productionJellyfinVolume+":/config", "--entrypoint", "/bin/sh", jellyfinImage, "-c", "rm -rf /config/* && chown -R "+strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid())+" /config")
	productionPort := liveFreePort(t)
	productionContainer := productionProject + "-bootstrap"
	productionCache := filepath.Join(temporary, "production-cache")
	if err := os.MkdirAll(productionCache, 0o750); err != nil {
		t.Fatal(err)
	}
	liveDocker(t, "run", "--detach", "--rm", "--name", productionContainer,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--publish", "127.0.0.1:"+strconv.Itoa(productionPort)+":8096",
		"--volume", productionJellyfinVolume+":/config",
		"--volume", productionCache+":/cache",
		"--volume", filepath.Join(productionData, "media")+":/data/media:ro",
		jellyfinImage,
	)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", productionContainer).Run() })
	productionClient := jellyfin.New("http://127.0.0.1:"+strconv.Itoa(productionPort), &http.Client{Timeout: 10 * time.Second})
	productionCredentials := jellyfin.Credentials{Username: "production-household", Password: "production-jellyfin-password"}
	deadline := time.Now().Add(2 * time.Minute)
	pollJellyfin(t, deadline, "initialize Production recovery state", func() (bool, error) {
		return true, productionClient.ReconcileLibraries(context.Background(), productionCredentials)
	})
	pollJellyfin(t, deadline, "index the Production movie", func() (bool, error) {
		return productionClient.MoviePlaybackReady(context.Background(), productionCredentials, "/data/media/movies/recovered-movie.mp4")
	})
	pollJellyfin(t, deadline, "index the Production episode", func() (bool, error) {
		return productionClient.EpisodePlaybackReady(context.Background(), productionCredentials, "/data/media/series/The Lucy Show/Season 01/The Lucy Show - S01E01.mp4")
	})
	liveDocker(t, "stop", productionContainer)

	backup := backupCommand(t, "--environment", "production", "--config", configPath, "--protect", "--output", "json")
	backupOutput, err := backup.CombinedOutput()
	if err != nil {
		t.Fatalf("create real Production backup: %v\n%s", err, backupOutput)
	}
	var backupReport struct {
		ManifestPath string `json:"manifestPath"`
	}
	if err := json.Unmarshal(backupOutput, &backupReport); err != nil {
		t.Fatalf("decode Production backup report: %v\n%s", err, backupOutput)
	}

	credentialsPath, runtimeDirectory, drillEnvironment := liveRestoreDrillCredentials(t, temporary)
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupReport.ManifestPath, "--as-restore-drill", "--credentials", credentialsPath)
	preview.Env = drillEnvironment
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("preview real Restore Drill:\n%s", output)
	}
	restore := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupReport.ManifestPath, "--as-restore-drill", "--credentials", credentialsPath, "--confirm", "--output", "json")
	restore.Env = drillEnvironment
	restoreOutput, err := restore.CombinedOutput()
	if err != nil {
		t.Fatalf("execute real Restore Drill: %v\n%s", err, restoreOutput)
	}

	stagingPort := liveConfiguredJellyfinPort(t, configPath)
	stagingClient := jellyfin.New("http://127.0.0.1:"+strconv.Itoa(stagingPort), &http.Client{Timeout: 10 * time.Second})
	drillCredentials := jellyfin.Credentials{Username: "drill-household", Password: "drill-jellyfin-password"}
	pollJellyfin(t, time.Now().Add(2*time.Minute), "serve recovered movie with rotated credentials", func() (bool, error) {
		return stagingClient.MoviePlaybackReady(context.Background(), drillCredentials, "/data/media/movies/recovered-movie.mp4")
	})
	pollJellyfin(t, time.Now().Add(2*time.Minute), "serve recovered episode with rotated credentials", func() (bool, error) {
		return stagingClient.EpisodePlaybackReady(context.Background(), drillCredentials, "/data/media/series/The Lucy Show/Season 01/The Lucy Show - S01E01.mp4")
	})

	running := strings.Fields(liveDocker(t, "ps", "--filter", "label=com.docker.compose.project="+stagingProject, "--format", "{{.Label \"com.docker.compose.service\"}}"))
	if len(running) != 1 || running[0] != "jellyfin" {
		t.Fatalf("running Staging Restore Drill services = %v, want only jellyfin", running)
	}
	if got := liveReadVolumeFile(t, jellyfinImage, stagingProject+"_profilarr-config"); got != stagingProject+"-profilarr" {
		t.Fatalf("Staging Profilarr state = %q, want preserved", got)
	}
	if got := liveReadVolumeFile(t, jellyfinImage, stagingProject+"_qbittorrent-config"); got != productionProject+"-qbittorrent" {
		t.Fatalf("recovered qBittorrent state = %q, want Production backup state", got)
	}
	if _, err := os.Stat(filepath.Join(runtimeDirectory, "media-stack", stagingProject, "openvpn_user")); err != nil {
		t.Fatalf("rotated Staging runtime credentials were not materialized: %v", err)
	}

	containers := strings.Fields(liveDocker(t, "ps", "-aq", "--filter", "label=com.docker.compose.project="+stagingProject))
	for _, container := range containers {
		_ = exec.Command("docker", "rm", "--force", container).Run()
	}
}

func liveRestoreDrillConfig(t *testing.T, repository, temporary, productionProject, stagingProject string) string {
	t.Helper()
	path := backupConfig(t, repository, temporary)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stagingPort := liveFreePort(t)
	replacements := []struct{ old, new string }{
		{"media-production", productionProject},
		{"media-staging", stagingProject},
		{"/srv/media/production", filepath.Join(temporary, "data", "production")},
		{"/srv/media/staging", filepath.Join(temporary, "data", "staging")},
		{"lanBindAddress: 0.0.0.0", "lanBindAddress: 127.0.0.1"},
		{"runtimeUID: 1000", "runtimeUID: " + strconv.Itoa(os.Getuid())},
		{"runtimeGID: 1000", "runtimeGID: " + strconv.Itoa(os.Getgid())},
		{"hardwareTranscoding: auto", "hardwareTranscoding: disabled"},
		{"jellyfin: 18096", "jellyfin: " + strconv.Itoa(stagingPort)},
	}
	configured := string(contents)
	for _, replacement := range replacements {
		configured = strings.ReplaceAll(configured, replacement.old, replacement.new)
	}
	writeFile(t, path, []byte(configured), 0o600)
	return path
}

func liveRestoreDrillCredentials(t *testing.T, temporary string) (string, string, []string) {
	t.Helper()
	credentialsPath := filepath.Join(temporary, "staging-drill.sops.yaml")
	writeFile(t, credentialsPath, []byte("encrypted: true\n"), 0o600)
	binDirectory := filepath.Join(temporary, "live-bin")
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
case "${4##*/}" in
	staging-drill.sops.yaml) prefix=drill ;;
	production.sops.yaml) prefix=production ;;
	*) prefix=staging ;;
esac
printf 'nordvpn:\n  openvpn:\n    serviceUsername: %s-user\n    servicePassword: %s-password\nprofilarr:\n  apiKey: %s-profilarr-api-key-32-characters\njellyfin:\n  username: %s-household\n  password: %s-jellyfin-password\nqbittorrent:\n  username: %s-household\n  password: %s-qbittorrent-password\n' "$prefix" "$prefix" "$prefix" "$prefix" "$prefix" "$prefix" "$prefix"
`), 0o700)
	runtimeDirectory := filepath.Join(temporary, "runtime")
	environment := append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+runtimeDirectory,
	)
	return credentialsPath, runtimeDirectory, environment
}

func liveCreateMediaFixture(t *testing.T, image, directory, name string) {
	t.Helper()
	liveDocker(t, "run", "--rm", "--entrypoint", "/usr/lib/jellyfin-ffmpeg/ffmpeg",
		"--volume", directory+":/fixture", image,
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "/fixture/"+name,
	)
}

func liveWriteVolumeFile(t *testing.T, image, volume, contents string) {
	t.Helper()
	liveDocker(t, "run", "--rm", "--user", "0:0", "--volume", volume+":/target", "--entrypoint", "/bin/sh", image, "-c", "printf '%s' \"$1\" > /target/identity.txt", "write-volume", contents)
}

func liveReadVolumeFile(t *testing.T, image, volume string) string {
	t.Helper()
	return liveDocker(t, "run", "--rm", "--volume", volume+":/source:ro", "--entrypoint", "/bin/cat", image, "/source/identity.txt")
}

func liveConfiguredJellyfinPort(t *testing.T, configPath string) int {
	t.Helper()
	declared, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return declared.Spec.Environments["staging"].Ports.Jellyfin
}

func liveFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func liveDocker(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("docker", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
