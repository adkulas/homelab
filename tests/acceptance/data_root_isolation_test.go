package acceptance_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPlanRejectsNestedEnvironmentDataRoots(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), &document); err != nil {
		t.Fatal(err)
	}
	spec := document["spec"].(map[string]any)
	environments := spec["environments"].(map[string]any)
	environments["production"].(map[string]any)["dataRoot"] = "/srv/media"
	environments["staging"].(map[string]any)["dataRoot"] = "/srv/media/staging"
	contents, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "media-stack.yaml")
	writeFile(t, configPath, contents, 0o600)

	output, err := planCommand(t, "--environment", "production", "--config", configPath).CombinedOutput()
	if err == nil {
		t.Fatalf("plan accepted nested Production and Staging data roots:\n%s", output)
	}
	if !strings.Contains(string(output), "data roots must not overlap") {
		t.Fatalf("nested-root failure is not actionable:\n%s", output)
	}
}
