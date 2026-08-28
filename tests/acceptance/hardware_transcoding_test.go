package acceptance_test

import (
	"reflect"
	"testing"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/hardware"
	"github.com/adkulas/homelab/internal/topology"
)

func TestDetectedHardwareTranscodingOverlayIsComposeValid(t *testing.T) {
	versions, err := config.LoadVersions(repositoryRoot(t) + "/stacks/media/versions.yaml")
	if err != nil {
		t.Fatalf("load image fixtures: %v", err)
	}
	rendered, err := topology.Render(
		config.Defaults{Timezone: "America/Toronto", RuntimeUID: 1000, RuntimeGID: 1000},
		config.Environment{ProjectName: "media-staging", DataRoot: "/srv/media/staging", Ports: config.Ports{QBittorrent: 18080, Prowlarr: 19696, Sonarr: 18989, Radarr: 17878, Profilarr: 16868, Jellyfin: 18096, Seerr: 15055}},
		config.VPN{},
		versions.Images,
		"/run/media-stack/media-staging",
		hardware.Transcoding{Status: hardware.TranscodingSupported, RenderDevice: "/dev/dri/renderD128", GroupID: 109},
	)
	if err != nil {
		t.Fatalf("render supported hardware overlay: %v", err)
	}

	project := mergedComposeProject(t, rendered)
	jellyfin := project.Services["jellyfin"]
	wantDevice := []composeDevice{{Source: "/dev/dri/renderD128", Target: "/dev/dri/renderD128", Permissions: "rwm"}}
	if !reflect.DeepEqual(jellyfin.Devices, wantDevice) {
		t.Errorf("Jellyfin devices = %#v, want %#v", jellyfin.Devices, wantDevice)
	}
	if !reflect.DeepEqual(jellyfin.GroupAdd, []string{"109"}) {
		t.Errorf("Jellyfin group_add = %#v, want [109]", jellyfin.GroupAdd)
	}
}
