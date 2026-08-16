package acceptance_test

import (
	"strings"
	"testing"
)

func TestPlanRejectsRuntimeVersionOverrides(t *testing.T) {
	command := planCommand(
		t,
		"--environment", "staging",
		"--config", "media-stack.yaml",
		"--versions", "versions.yaml",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack plan accepted a runtime versions override\n%s", output)
	}
	if !strings.Contains(string(output), "flag provided but not defined: -versions") {
		t.Fatalf("media-stack plan reported the wrong error:\n%s", output)
	}
}
