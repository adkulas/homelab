package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPlanRendersCheckedInImagesForSelectedEnvironment(t *testing.T) {
	command := planCommand(t, "--environment", "staging")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack plan failed: %v\n%s", err, output)
	}

	want := map[string]string{
		"calibre-web":    "ghcr.io/crocodilestick/calibre-web-automated@sha256:5e00373854247750cc3e4479b492ae09293ff5e06ed10177f226634d97888679",
		"audiobookshelf": "ghcr.io/advplyr/audiobookshelf@sha256:60064c23f32fd9099fd39e82c5132c51701d1221135bbca04cd099b589e1edc3",
		"houndarr":       "ghcr.io/av1155/houndarr@sha256:b2e4ff8b550d385a56ea3380bddf88253eac00d750a811a8f97090ff77dcc79e",
		"cleanuparr":     "ghcr.io/cleanuparr/cleanuparr@sha256:923c2fe435e22fc58d261bacd2d6bbd8eb1ba3f95358803a8e4d3287ffd919d3",
		"gluetun":        "ghcr.io/qdm12/gluetun@sha256:e3272b29a4bc177b389fbdcb54cf9716ccbfc30f04d8b7a35b0a5be9cdb58461",
		"jellyfin":       "jellyfin/jellyfin@sha256:fea863d0cd9ec445985d9c75fc6ed64e38a1d2766cf0c250a33d553f4fc1b771",
		"profilarr":      "ghcr.io/dictionarry-hub/profilarr@sha256:352e42dec74ecfd68d13bb198fbc6d9346e3a8c55003026e1814e59319a2176d",
		"prowlarr":       "lscr.io/linuxserver/prowlarr@sha256:2806c9b763810589c42157730d547c293639e55c7610e9f5069314854639ec48",
		"qbittorrent":    "lscr.io/linuxserver/qbittorrent@sha256:a8083a3337a175e84374e3c8cf34ab76566c54502a2e8580e53b6deaa5b2f619",
		"radarr":         "lscr.io/linuxserver/radarr@sha256:044788f5508b7f67a3e0368cdd142c77c4d4cd5aec2efaba53e1a68b6b6231e7",
		"seerr":          "ghcr.io/seerr-team/seerr@sha256:aadee014b40e28cf9aa4928bc9d1183a6eb08f8c44d543cddc9efdcb16549be2",
		"sonarr":         "lscr.io/linuxserver/sonarr@sha256:339814bbf50599ac3b59077c252f29833d0138b59a062d0e82ce37bdc7f04f86",
	}
	if got := renderedImages(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered images = %#v, want %#v\nrendered Compose:\n%s", got, want, output)
	}
}

func planCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	if !slices.Contains(arguments, "--config") && !slices.Contains(arguments, "--site") {
		arguments = append(arguments, "--config", filepath.Join(repositoryRoot(t), "tests/fixtures/media-stack.yaml"))
	}
	goArguments := append([]string{"run", "../../cmd/media-stack", "plan"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "services", "media")
	return command
}

func renderedImages(compose []byte) map[string]string {
	var project struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &project); err != nil {
		return nil
	}
	images := make(map[string]string, len(project.Services))
	for name, service := range project.Services {
		images[name] = service.Image
	}
	return images
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate acceptance test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
func TestPlanReportsDeclaredTopologyDriftAndRetainsUnknownProjectResources(t *testing.T) {
	temporary := t.TempDir()
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	dockerLog := filepath.Join(temporary, "docker.log")
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$PLAN_DOCKER_LOG"
case "$*" in
  "compose -f - config --hash *")
    cat >/dev/null
    printf '%s\n' 'qbittorrent declared-qbittorrent-hash'
    ;;
  "ps --all --filter label=com.docker.compose.project=media-staging --format {{json .}}")
    printf '%s\n' '{"Labels":"com.docker.compose.project=media-staging,com.docker.compose.service=qbittorrent,com.docker.compose.config-hash=drifted","Names":"media-staging-qbittorrent-1","State":"running"}'
    printf '%s\n' '{"Image":"alpine:3.22","Labels":"com.docker.compose.project=media-staging,com.docker.compose.service=debug-shell","Names":"media-staging-debug-shell-1","State":"running"}'
    ;;
  "network ls --filter label=com.docker.compose.project=media-staging --format {{json .}}")
    printf '%s\n' '{"Labels":"com.docker.compose.project=media-staging,com.docker.compose.network=debug","Name":"media-staging_debug"}'
    ;;
  "volume ls --filter label=com.docker.compose.project=media-staging --format {{json .}}")
    printf '%s\n' '{"Labels":"com.docker.compose.project=media-staging,com.docker.compose.volume=scratch","Name":"media-staging_scratch"}'
    ;;
  *) exit 99 ;;
esac
`), 0o700)

	command := planCommand(t, "--environment", "staging")
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PLAN_DOCKER_LOG="+dockerLog,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack plan failed: %v\n%s", err, output)
	}

	for _, want := range []string{
		"# create gluetun: declared service is absent",
		"# update qbittorrent: rendered configuration differs from Declared Configuration",
		"# unknown service/debug-shell: project resource is outside the Service Configuration Contract; retained",
		"# unknown network/debug: project resource is outside the Service Configuration Contract; retained",
		"# unknown volume/scratch: project resource is outside the Service Configuration Contract; retained",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("plan output does not contain %q:\n%s", want, output)
		}
	}
	wantDocker := "compose -f - config --hash *\n" +
		"ps --all --filter label=com.docker.compose.project=media-staging --format {{json .}}\n" +
		"network ls --filter label=com.docker.compose.project=media-staging --format {{json .}}\n" +
		"volume ls --filter label=com.docker.compose.project=media-staging --format {{json .}}"
	if got := strings.TrimSpace(string(readFile(t, dockerLog))); got != wantDocker {
		t.Errorf("plan Docker calls = %q, want one read-only project observation", got)
	}
}
