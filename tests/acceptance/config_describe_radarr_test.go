package acceptance_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigDescribeExplainsRadarrNamingOwnership(t *testing.T) {
	output, err := configDescribeCommand(t, "--service", "radarr", "--output", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack config describe failed: %v\n%s", err, output)
	}
	var document contractOutput
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode configuration contract: %v", err)
	}
	for _, setting := range document.Services[0].Settings {
		if setting.ID != "naming.standardMovieFormat" {
			continue
		}
		if setting.Control != "externally-synchronized" || setting.Source != "stacks/media/fixtures/profilarr-movie-policy.yaml#naming.standardMovieFormat" || setting.Owner != "Profilarr pinned policy" {
			t.Fatalf("Radarr naming ownership = %#v", setting)
		}
		if !strings.Contains(strings.ToLower(setting.OperatorChange), "not configurable through media-stack.yaml") {
			t.Fatalf("operatorChange = %q", setting.OperatorChange)
		}
		return
	}
	t.Fatal("Radarr omitted naming.standardMovieFormat")
}
