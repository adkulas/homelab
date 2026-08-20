package acceptance_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentationExplainsLocalBootstrapAndDeclaredDataRoot(t *testing.T) {
	readme := string(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "README.md")))
	contributing := string(readFile(t, filepath.Join(repositoryRoot(t), "CONTRIBUTING.md")))

	for _, want := range []string{
		"go build -o bin/media-stack ./cmd/media-stack",
		"./bin/media-stack init --environment staging",
		"`setup.sh` expects a published GitHub Release asset",
		"`dataRoot` is not prompted by `init`",
		"declared path in the checked-in configuration",
	} {
		if !strings.Contains(readme, want) && !strings.Contains(contributing, want) {
			t.Fatalf("documentation does not contain %q", want)
		}
	}
}
