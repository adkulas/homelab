package acceptance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type contractOutput struct {
	SchemaVersion string `json:"schemaVersion"`
	Services      []struct {
		Name      string            `json:"name"`
		Settings  []contractSetting `json:"settings"`
		Unmanaged string            `json:"unmanaged"`
	} `json:"services"`
}

type contractSetting struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Control        string   `json:"control"`
	Description    string   `json:"description"`
	Source         string   `json:"source"`
	Owner          string   `json:"owner"`
	Type           string   `json:"type"`
	AllowedValues  []string `json:"allowedValues"`
	Default        any      `json:"default"`
	Sensitive      bool     `json:"sensitive"`
	Lifecycle      []string `json:"lifecycle"`
	Status         string   `json:"status"`
	OperatorChange string   `json:"operatorChange"`
}

func TestConfigDescribePublishesACompleteValidatedContract(t *testing.T) {
	output, err := configDescribeCommand(t, "--output", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack config describe failed: %v\n%s", err, output)
	}
	var document contractOutput
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode configuration contract: %v", err)
	}
	controls := map[string]bool{}
	sources := map[string]bool{}
	for _, service := range document.Services {
		if service.Unmanaged == "" || len(service.Settings) == 0 {
			t.Fatalf("service %s is not self-contained", service.Name)
		}
		ids := make([]string, 0, len(service.Settings))
		for _, setting := range service.Settings {
			ids = append(ids, setting.ID)
			controls[setting.Control] = true
			sources[setting.Source] = true
			if setting.Name == "" || setting.Description == "" || setting.Source == "" || setting.Status != "implemented" || setting.OperatorChange == "" || len(setting.Lifecycle) == 0 {
				t.Fatalf("incomplete setting %s.%s: %#v", service.Name, setting.ID, setting)
			}
			if setting.Control == "secret" && (!setting.Sensitive || setting.Default != nil) {
				t.Fatalf("secret %s.%s is not safely described: %#v", service.Name, setting.ID, setting)
			}
		}
		if !sort.StringsAreSorted(ids) {
			t.Fatalf("settings for %s are not stable: %#v", service.Name, ids)
		}
	}
	wantControls := map[string]bool{"declared": true, "secret": true, "derived": true, "fixed": true, "externally-synchronized": true, "unmanaged": true}
	if !reflect.DeepEqual(controls, wantControls) {
		t.Fatalf("control classes = %#v, want %#v", controls, wantControls)
	}
	for _, source := range []string{
		"media-stack.yaml#spec.defaults.timezone",
		"media-stack.yaml#spec.defaults.runtimeUID",
		"media-stack.yaml#spec.defaults.runtimeGID",
		"media-stack.yaml#spec.defaults.lanBindAddress",
		"media-stack.yaml#spec.defaults.backupRetention.daily",
		"media-stack.yaml#spec.defaults.backupRetention.weekly",
		"media-stack.yaml#spec.defaults.backupRetention.monthly",
		"media-stack.yaml#spec.environments.<environment>.projectName",
		"media-stack.yaml#spec.environments.<environment>.dataRoot",
		"media-stack.yaml#spec.environments.<environment>.secretsFile",
		"media-stack.yaml#spec.environments.<environment>.backupRoot",
		"media-stack.yaml#spec.environments.<environment>.hardwareTranscoding",
		"media-stack.yaml#spec.environments.<environment>.ports.qbittorrent",
		"media-stack.yaml#spec.environments.<environment>.ports.prowlarr",
		"media-stack.yaml#spec.environments.<environment>.ports.sonarr",
		"media-stack.yaml#spec.environments.<environment>.ports.radarr",
		"media-stack.yaml#spec.environments.<environment>.ports.profilarr",
		"media-stack.yaml#spec.environments.<environment>.ports.jellyfin",
		"media-stack.yaml#spec.environments.<environment>.ports.seerr",
		"media-stack.yaml#spec.acquisition.vpn.provider",
		"media-stack.yaml#spec.acquisition.vpn.protocol",
		"media-stack.yaml#spec.acquisition.vpn.openvpnProtocol",
		"media-stack.yaml#spec.acquisition.vpn.server.countries",
		"media-stack.yaml#spec.acquisition.vpn.server.categories",
		"media-stack.yaml#spec.acquisition.vpn.catalogueUpdateInterval",
		"media-stack.yaml#spec.acquisition.publicTorrentSources[].id",
		"media-stack.yaml#spec.acquisition.publicTorrentSources[].enabled",
		"versions.yaml#policy.profilarrPcdRevision",
	} {
		if !sources[source] {
			t.Errorf("supported field source %q is absent", source)
		}
	}
	for _, service := range []string{"gluetun", "qbittorrent", "prowlarr", "sonarr", "radarr", "profilarr", "jellyfin", "seerr"} {
		if !sources["versions.yaml#images."+service] {
			t.Errorf("versions image source for %s is absent", service)
		}
	}
}

func TestConfigDescribeFiltersEveryServiceIndependently(t *testing.T) {
	for _, name := range []string{"gluetun", "jellyfin", "profilarr", "prowlarr", "qbittorrent", "radarr", "seerr", "sonarr"} {
		t.Run(name, func(t *testing.T) {
			output, err := configDescribeCommand(t, "--service", name, "--output", "json").CombinedOutput()
			if err != nil {
				t.Fatalf("filter failed: %v\n%s", err, output)
			}
			var document contractOutput
			if err := json.Unmarshal(output, &document); err != nil || len(document.Services) != 1 || document.Services[0].Name != name {
				t.Fatalf("filtered output = %s (decode error %v)", output, err)
			}
		})
	}
}

func TestConfigDescribeJSONIsDeterministic(t *testing.T) {
	first, err := configDescribeCommand(t, "--output", "json").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	second, err := configDescribeCommand(t, "--output", "json").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("configuration contract JSON changed between identical invocations")
	}
}

func TestConfigDescribeNeedsNoEnvironmentOrExternalSystems(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "media-stack")
	build := exec.Command("go", "build", "-o", binary, "./cmd/media-stack")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build media-stack: %v\n%s", err, output)
	}
	empty := t.TempDir()
	command := exec.Command(binary, "config", "describe", "--service", "gluetun", "--output", "json")
	command.Dir = empty
	command.Env = []string{"PATH="}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("standalone describe failed: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(empty)
	if err != nil || len(entries) != 0 {
		t.Fatalf("config describe mutated its working directory: %#v, %v", entries, err)
	}
}

func TestConfigDescribeReportsStableUsageForUnknownService(t *testing.T) {
	output, err := configDescribeCommand(t, "--service", "lidarr").CombinedOutput()
	if err == nil {
		t.Fatalf("unknown service succeeded:\n%s", output)
	}
	want := "unknown service \"lidarr\"; supported services: gluetun, jellyfin, profilarr, prowlarr, qbittorrent, radarr, seerr, sonarr"
	if !strings.Contains(string(output), want) {
		t.Fatalf("unknown-service error = %s", output)
	}
}
