package acceptance_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHomepageQbittorrentActivity(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for the Homepage transfer summary test")
	}
	root := repositoryRoot(t)
	test := filepath.Join(root, "tests", "acceptance", "testdata", "homepage_qbittorrent_test.py")
	adapter := filepath.Join(root, "sites", "home", "homepage", "qbittorrent-activity.py")
	if output, err := exec.Command(python, "-E", test, adapter).CombinedOutput(); err != nil {
		t.Fatalf("Homepage qBittorrent activity: %v\n%s", err, output)
	}
}
