package acceptance_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type configurationContractDocument struct {
	SchemaVersion string `json:"schemaVersion"`
	Services      []struct {
		Name string `json:"name"`
	} `json:"services"`
}

func TestConfigDescribeReturnsEveryServiceInStableOrder(t *testing.T) {
	command := configDescribeCommand(t, "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack config describe failed: %v\n%s", err, output)
	}

	var document configurationContractDocument
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode configuration contract: %v\n%s", err, output)
	}
	if document.SchemaVersion != "homelab.media-stack/configuration-contract/v1alpha1" {
		t.Fatalf("schemaVersion = %q", document.SchemaVersion)
	}
	var names []string
	for _, service := range document.Services {
		names = append(names, service.Name)
	}
	want := []string{"gluetun", "jellyfin", "profilarr", "prowlarr", "qbittorrent", "radarr", "seerr", "sonarr"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("services = %#v, want %#v", names, want)
	}
}

func configDescribeCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	goArguments := append([]string{"run", "../../cmd/media-stack", "config", "describe"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
	return command
}
