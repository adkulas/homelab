package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigDescribeExplainsSonarrNamingOwnership(t *testing.T) {
	command := configDescribeCommand(t, "--service", "sonarr", "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack config describe failed: %v\n%s", err, output)
	}
	var document struct {
		Services []struct {
			Name      string `json:"name"`
			Unmanaged string `json:"unmanaged"`
			Settings  []struct {
				ID             string `json:"id"`
				Control        string `json:"control"`
				Source         string `json:"source"`
				Owner          string `json:"owner"`
				OperatorChange string `json:"operatorChange"`
			} `json:"settings"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode configuration contract: %v", err)
	}
	if len(document.Services) != 1 || document.Services[0].Name != "sonarr" {
		t.Fatalf("filtered services = %#v", document.Services)
	}
	service := document.Services[0]
	if service.Unmanaged == "" {
		t.Fatal("Sonarr omitted the Unmanaged Configuration statement")
	}
	for _, setting := range service.Settings {
		if setting.ID != "naming.standardEpisodeFormat" {
			continue
		}
		if setting.Control != "externally-synchronized" || setting.Source != "stacks/media/fixtures/profilarr-series-policy.yaml#naming.standardEpisodeFormat" || setting.Owner != "Profilarr pinned policy" {
			t.Fatalf("Sonarr naming ownership = %#v", setting)
		}
		if !strings.Contains(strings.ToLower(setting.OperatorChange), "not configurable through media-stack.yaml") {
			t.Fatalf("operatorChange = %q", setting.OperatorChange)
		}
		return
	}
	t.Fatal("Sonarr omitted naming.standardEpisodeFormat")
}
