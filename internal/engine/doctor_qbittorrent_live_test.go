package engine

import (
	"context"
	"os"
	"testing"

	"github.com/adkulas/homelab/internal/config"
)

func TestPinnedQBittorrentBootstrapRestartAndPeerContract(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_QBITTORRENT") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_QBITTORRENT=1 on a Docker-enabled host")
	}
	versions, err := config.LoadVersions("../../stacks/media/versions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := doctorQBittorrentBootstrap(context.Background(), versions.Images["qbittorrent"], os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("pinned qBittorrent contract: %v", err)
	}
}
