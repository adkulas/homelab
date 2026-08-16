package acceptance_test

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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
		"gluetun":     "ghcr.io/qdm12/gluetun@sha256:e3272b29a4bc177b389fbdcb54cf9716ccbfc30f04d8b7a35b0a5be9cdb58461",
		"jellyfin":    "jellyfin/jellyfin@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db",
		"profilarr":   "ghcr.io/dictionarry-hub/profilarr@sha256:75a43c9c19c70f6e48315d4ed5cef3232d905da8fab397391a2078a5e0fd7ec1",
		"prowlarr":    "lscr.io/linuxserver/prowlarr@sha256:1295cff29d10b486c0d8324d1559a552140a5932bf8b3d87e398654414f63f92",
		"qbittorrent": "lscr.io/linuxserver/qbittorrent@sha256:6816d2b144b1eb97665f886e41e18a14d026ba78c9d0953fc68a1211ea819433",
		"radarr":      "lscr.io/linuxserver/radarr@sha256:a45b5ab0f850f39edb4cc9c95bbd967b52ddc3d4574a4dfb45561177db6c88f4",
		"seerr":       "ghcr.io/seerr-team/seerr@sha256:f4768de5f616248d723e05891f3345a1402123775d03bf0890dbfedc0831bda1",
		"sonarr":      "lscr.io/linuxserver/sonarr@sha256:373159ba768e23a3a1c497d9f2b936addf8fd5b1fdce7dd6a14080ac928bfda0",
	}
	if got := renderedImages(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered images = %#v, want %#v\nrendered Compose:\n%s", got, want, output)
	}
}

func planCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	goArguments := append([]string{"run", "../../cmd/media-stack", "plan"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
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
