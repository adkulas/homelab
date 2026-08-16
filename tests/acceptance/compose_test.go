package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

func TestPlanRendersSchemaValidPortableServiceDefaults(t *testing.T) {
	plan := planCommand(t, "--environment", "staging")
	rendered, err := plan.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack plan failed: %v\n%s", err, rendered)
	}

	validation := exec.Command(
		"docker", "compose",
		"--project-name", "media-staging",
		"-f", "-",
		"config",
		"--format", "json",
	)
	validation.Dir = repositoryRoot(t)
	validation.Stdin = bytes.NewReader(rendered)
	merged, err := validation.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config rejected rendered output: %v\n%s\nrendered Compose:\n%s", err, merged, rendered)
	}

	var project composeProject
	if err := json.Unmarshal(merged, &project); err != nil {
		t.Fatalf("decode merged Compose project: %v\n%s", err, merged)
	}

	identity := map[string]identityContract{
		"gluetun":     inheritedIdentity,
		"jellyfin":    composeUserIdentity,
		"profilarr":   puidIdentity,
		"prowlarr":    puidIdentity,
		"qbittorrent": puidIdentity,
		"radarr":      puidIdentity,
		"seerr":       composeUserIdentity,
		"sonarr":      puidIdentity,
	}
	var problems []string
	for name, identityContract := range identity {
		service, exists := project.Services[name]
		if !exists {
			problems = append(problems, fmt.Sprintf("%s is missing", name))
			continue
		}
		if service.ContainerName != "" {
			problems = append(problems, fmt.Sprintf("%s fixes container_name to %q", name, service.ContainerName))
		}
		if service.Restart != "unless-stopped" {
			problems = append(problems, fmt.Sprintf("%s restart = %q", name, service.Restart))
		}
		if service.Logging.Driver != "json-file" ||
			service.Logging.Options["max-size"] != "10m" ||
			service.Logging.Options["max-file"] != "3" {
			problems = append(problems, fmt.Sprintf("%s logging = %#v", name, service.Logging))
		}
		if service.Environment["TZ"] != "America/Toronto" {
			problems = append(problems, fmt.Sprintf("%s TZ = %q", name, service.Environment["TZ"]))
		}
		switch identityContract {
		case puidIdentity:
			if service.Environment["PUID"] != "1000" || service.Environment["PGID"] != "1000" {
				problems = append(problems, fmt.Sprintf("%s PUID:PGID = %q:%q", name, service.Environment["PUID"], service.Environment["PGID"]))
			}
		case composeUserIdentity:
			if service.User != "1000:1000" {
				problems = append(problems, fmt.Sprintf("%s user = %q", name, service.User))
			}
		}
	}
	for name, volume := range project.Volumes {
		if !strings.HasPrefix(volume.Name, "media-staging_") {
			problems = append(problems, fmt.Sprintf("volume %s has unqualified name %q", name, volume.Name))
		}
	}
	sort.Strings(problems)
	if len(problems) != 0 {
		t.Fatalf("merged Compose project violates portable defaults:\n%s\nrendered Compose:\n%s", strings.Join(problems, "\n"), rendered)
	}
}

type identityContract uint8

const (
	inheritedIdentity identityContract = iota
	puidIdentity
	composeUserIdentity
)

type composeProject struct {
	Services map[string]composeService `json:"services"`
	Volumes  map[string]composeVolume  `json:"volumes"`
}

type composeService struct {
	ContainerName string            `json:"container_name"`
	Environment   map[string]string `json:"environment"`
	Logging       composeLogging    `json:"logging"`
	Restart       string            `json:"restart"`
	User          string            `json:"user"`
}

type composeLogging struct {
	Driver  string            `json:"driver"`
	Options map[string]string `json:"options"`
}

type composeVolume struct {
	Name string `json:"name"`
}
