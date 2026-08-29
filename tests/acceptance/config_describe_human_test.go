package acceptance_test

import (
	"strings"
	"testing"
)

func TestConfigDescribeHumanOutputAnswersHowToChangeSonarrNaming(t *testing.T) {
	output, err := configDescribeCommand(t, "--service", "sonarr").CombinedOutput()
	if err != nil {
		t.Fatalf("human config describe failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"Sonarr",
		"DECLARED",
		"SECRETS",
		"EXTERNALLY SYNCHRONIZED",
		"DERIVED",
		"FIXED",
		"UNMANAGED",
		"naming.standardEpisodeFormat — Standard episode format",
		"control: externally-synchronized",
		"source: stacks/media/fixtures/profilarr-series-policy.yaml#naming.standardEpisodeFormat",
		"owner: Profilarr pinned policy",
		"lifecycle: synchronize, verify",
		"operator change: Not configurable through media-stack.yaml",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("human output omitted %q:\n%s", want, text)
		}
	}
	order := []string{"DECLARED", "SECRETS", "EXTERNALLY SYNCHRONIZED", "DERIVED", "FIXED", "UNMANAGED"}
	last := -1
	for _, heading := range order {
		position := strings.Index(text, heading)
		if position <= last {
			t.Fatalf("control groups are not in required order: %v\n%s", order, text)
		}
		last = position
	}
}
