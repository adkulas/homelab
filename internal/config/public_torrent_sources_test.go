package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAcceptsOnlyCataloguedPublicTorrentSources(t *testing.T) {
	valid := `apiVersion: homelab.media-stack/v1alpha1
kind: MediaStack
spec:
  acquisition:
    publicTorrentSources:
      - id: internetarchive
        enabled: true
`
	path := filepath.Join(t.TempDir(), "media-stack.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	declared, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := declared.Spec.Acquisition.PublicTorrentSources; len(got) != 1 || got[0].ID != "internetarchive" || !got[0].Enabled {
		t.Fatalf("Public Torrent Sources = %#v", got)
	}

	unknown := strings.Replace(valid, "internetarchive", "unreviewed-source", 1)
	if err := os.WriteFile(path, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "not in the approved catalog") {
		t.Fatalf("Load unknown Public Torrent Source error = %v", err)
	}
}
