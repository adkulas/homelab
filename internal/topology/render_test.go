package topology_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/hardware"
	"github.com/adkulas/homelab/internal/topology"
	"gopkg.in/yaml.v3"
)

func TestRenderUsesEnvironmentQBittorrentPortEndToEnd(t *testing.T) {
	const port = 18080
	rendered, err := topology.Render(
		config.Defaults{Timezone: "America/Toronto", RuntimeUID: 1000, RuntimeGID: 1000, LANBindAddress: "127.0.0.1"},
		config.Environment{ProjectName: "media-staging", DataRoot: "/srv/media/staging", Ports: config.Ports{QBittorrent: port}},
		config.VPN{}, imageFixtures(), "/run/media-stack/media-staging", hardware.Transcoding{},
	)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	var project struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Ports       []string          `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(rendered, &project); err != nil {
		t.Fatalf("decode rendered Compose: %v", err)
	}
	wantPort := strconv.Itoa(port)
	if got := project.Services["qbittorrent"].Environment["WEBUI_PORT"]; got != wantPort {
		t.Errorf("qBittorrent WEBUI_PORT = %q, want %q", got, wantPort)
	}
	if got := project.Services["gluetun"].Environment["FIREWALL_INPUT_PORTS"]; got != wantPort {
		t.Errorf("Gluetun FIREWALL_INPUT_PORTS = %q, want %q", got, wantPort)
	}
	if got := project.Services["gluetun"].Ports; !reflect.DeepEqual(got, []string{"127.0.0.1:18080:18080"}) {
		t.Errorf("Gluetun ports = %#v, want canonical qBittorrent port", got)
	}
}

func TestRenderAppliesDetectedHardwareTranscodingOverlay(t *testing.T) {
	tests := []struct {
		name        string
		transcoding hardware.Transcoding
		wantDevices []string
		wantGroups  []string
	}{
		{
			name:        "supported host",
			transcoding: hardware.Transcoding{Status: hardware.TranscodingSupported, RenderDevice: "/dev/dri/renderD128", GroupID: 109},
			wantDevices: []string{"/dev/dri/renderD128:/dev/dri/renderD128"},
			wantGroups:  []string{"109"},
		},
		{name: "unsupported host"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := topology.Render(
				config.Defaults{Timezone: "America/Toronto", RuntimeUID: 1000, RuntimeGID: 1000},
				config.Environment{ProjectName: "media-staging", DataRoot: "/srv/media/staging"},
				config.VPN{},
				imageFixtures(),
				"/run/media-stack/media-staging",
				test.transcoding,
			)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			var project struct {
				Services map[string]struct {
					Devices  []string `yaml:"devices"`
					GroupAdd []string `yaml:"group_add"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal(rendered, &project); err != nil {
				t.Fatalf("decode rendered Compose: %v", err)
			}
			jellyfin := project.Services["jellyfin"]
			if !reflect.DeepEqual(jellyfin.Devices, test.wantDevices) {
				t.Errorf("Jellyfin devices = %#v, want %#v", jellyfin.Devices, test.wantDevices)
			}
			if !reflect.DeepEqual(jellyfin.GroupAdd, test.wantGroups) {
				t.Errorf("Jellyfin group_add = %#v, want %#v", jellyfin.GroupAdd, test.wantGroups)
			}
		})
	}
}

func imageFixtures() map[string]string {
	const image = "example.invalid/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return map[string]string{
		"gluetun": image, "qbittorrent": image, "prowlarr": image, "sonarr": image,
		"radarr": image, "profilarr": image, "jellyfin": image, "seerr": image,
	}
}
