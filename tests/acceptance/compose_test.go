package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestPlanSelectsDistinctComposeProjectNames(t *testing.T) {
	want := map[string]string{
		"production": "media-production",
		"staging":    "media-staging",
	}

	for environment, projectName := range want {
		t.Run(environment, func(t *testing.T) {
			rendered, err := planCommand(t, "--environment", environment).CombinedOutput()
			if err != nil {
				t.Fatalf("media-stack plan failed: %v\n%s", err, rendered)
			}

			var project struct {
				Name string `yaml:"name"`
			}
			if err := yaml.Unmarshal(rendered, &project); err != nil {
				t.Fatalf("decode rendered Compose: %v\n%s", err, rendered)
			}
			if project.Name != projectName {
				t.Fatalf("Compose project name = %q, want %q\nrendered Compose:\n%s", project.Name, projectName, rendered)
			}
		})
	}
}

func TestPlanRendersCoexistingEnvironmentResources(t *testing.T) {
	type expectedEnvironment struct {
		projectName string
		dataRoot    string
		secretFile  string
		ports       map[string]composePort
	}
	want := map[string]expectedEnvironment{
		"production": {
			projectName: "media-production",
			dataRoot:    "/srv/media/production",
			secretFile:  "secrets/production.sops.yaml",
			ports: map[string]composePort{
				"gluetun": {Published: "8080", Target: 8080}, "prowlarr": {Published: "9696", Target: 9696},
				"sonarr": {Published: "8989", Target: 8989}, "radarr": {Published: "7878", Target: 7878},
				"profilarr": {Published: "6868", Target: 6868}, "jellyfin": {Published: "8096", Target: 8096},
				"seerr": {Published: "5055", Target: 5055},
			},
		},
		"staging": {
			projectName: "media-staging",
			dataRoot:    "/srv/media/staging",
			secretFile:  "secrets/staging.sops.yaml",
			ports: map[string]composePort{
				"gluetun": {Published: "18080", Target: 8080}, "prowlarr": {Published: "19696", Target: 9696},
				"sonarr": {Published: "18989", Target: 8989}, "radarr": {Published: "17878", Target: 7878},
				"profilarr": {Published: "16868", Target: 6868}, "jellyfin": {Published: "18096", Target: 8096},
				"seerr": {Published: "15055", Target: 5055},
			},
		},
	}

	resourceNames := make(map[string]map[string]struct{}, len(want))
	for environment, expected := range want {
		t.Run(environment, func(t *testing.T) {
			rendered, err := planCommand(t, "--environment", environment).CombinedOutput()
			if err != nil {
				t.Fatalf("media-stack plan failed: %v\n%s", err, rendered)
			}
			project := mergedComposeProject(t, rendered)

			var problems []string
			if project.Name != expected.projectName {
				problems = append(problems, fmt.Sprintf("project name = %q, want %q", project.Name, expected.projectName))
			}
			for serviceName, port := range expected.ports {
				service := project.Services[serviceName]
				if len(service.Ports) != 1 || service.Ports[0].Published != port.Published || service.Ports[0].Target != port.Target {
					problems = append(problems, fmt.Sprintf("%s published ports = %#v, want %#v", serviceName, service.Ports, port))
				}
			}
			wantDataMounts := map[string]composeMount{
				"qbittorrent": {Type: "bind", Source: expected.dataRoot + "/torrents", Target: "/data/torrents"},
				"radarr":      {Type: "bind", Source: expected.dataRoot, Target: "/data"},
				"sonarr":      {Type: "bind", Source: expected.dataRoot, Target: "/data"},
				"jellyfin":    {Type: "bind", Source: expected.dataRoot + "/media", Target: "/data/media", ReadOnly: true},
			}
			for serviceName, mount := range wantDataMounts {
				if !containsMount(project.Services[serviceName].Volumes, mount) {
					problems = append(problems, fmt.Sprintf("%s mounts = %#v, missing %#v", serviceName, project.Services[serviceName].Volumes, mount))
				}
			}
			secret, exists := project.Secrets["environment"]
			if !exists || !strings.HasSuffix(secret.File, expected.secretFile) {
				problems = append(problems, fmt.Sprintf("environment secret = %#v, want file suffix %q", secret, expected.secretFile))
			}

			resourceNames[environment] = map[string]struct{}{}
			for _, resources := range []map[string]composeResource{project.Networks, project.Volumes, project.Secrets} {
				for _, resource := range resources {
					if !strings.HasPrefix(resource.Name, expected.projectName+"_") {
						problems = append(problems, fmt.Sprintf("resource name %q is not scoped by %q", resource.Name, expected.projectName))
					}
					resourceNames[environment][resource.Name] = struct{}{}
				}
			}
			if len(project.Networks) != 1 {
				problems = append(problems, fmt.Sprintf("networks = %#v, want one application network", project.Networks))
			}
			if _, exists := project.Networks["application"]; !exists {
				problems = append(problems, fmt.Sprintf("networks = %#v, want application network", project.Networks))
			}
			sort.Strings(problems)
			if len(problems) != 0 {
				t.Fatalf("merged Compose project violates environment isolation:\n%s\nrendered Compose:\n%s", strings.Join(problems, "\n"), rendered)
			}
		})
	}

	for name := range resourceNames["production"] {
		if _, collision := resourceNames["staging"][name]; collision {
			t.Errorf("Production and Staging both render resource name %q", name)
		}
	}
}

func mergedComposeProject(t *testing.T, rendered []byte) composeProject {
	t.Helper()
	validation := exec.Command("docker", "compose", "-f", "-", "config", "--format", "json")
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
	return project
}

func containsMount(mounts []composeMount, want composeMount) bool {
	for _, mount := range mounts {
		if mount.Type == want.Type && mount.Source == want.Source && mount.Target == want.Target && mount.ReadOnly == want.ReadOnly {
			return true
		}
	}
	return false
}

type identityContract uint8

const (
	inheritedIdentity identityContract = iota
	puidIdentity
	composeUserIdentity
)

type composeProject struct {
	Name     string                     `json:"name"`
	Services map[string]composeService  `json:"services"`
	Networks map[string]composeResource `json:"networks"`
	Secrets  map[string]composeResource `json:"secrets"`
	Volumes  map[string]composeResource `json:"volumes"`
}

type composeService struct {
	ContainerName string            `json:"container_name"`
	Environment   map[string]string `json:"environment"`
	Logging       composeLogging    `json:"logging"`
	Restart       string            `json:"restart"`
	User          string            `json:"user"`
	Ports         []composePort     `json:"ports"`
	Volumes       []composeMount    `json:"volumes"`
}

type composeLogging struct {
	Driver  string            `json:"driver"`
	Options map[string]string `json:"options"`
}

type composePort struct {
	Published string `json:"published"`
	Target    int    `json:"target"`
}

type composeMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type composeResource struct {
	Name string `json:"name"`
	File string `json:"file"`
}
