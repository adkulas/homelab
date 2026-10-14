package acceptance_test

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type traefikRouter struct {
	Rule        string   `yaml:"rule"`
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
}

type traefikServer struct {
	URL     string `yaml:"url"`
	Address string `yaml:"address"`
}

type traefikService struct {
	LoadBalancer struct {
		Servers []traefikServer `yaml:"servers"`
	} `yaml:"loadBalancer"`
}

func TestHomeTraefikRoutesTargetNamedHosts(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "sites", "home", "traefik")
	var media struct {
		HTTP struct {
			Routers  map[string]traefikRouter  `yaml:"routers"`
			Services map[string]traefikService `yaml:"services"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(readFile(t, filepath.Join(root, "dynamic", "media-stack.yml")), &media); err != nil {
		t.Fatalf("decode Media Stack routes: %v", err)
	}
	want := map[string]int{
		"qbittorrent.home": 8080, "prowlarr.home": 9696, "sonarr.home": 8989,
		"radarr.home": 7878, "profilarr.home": 6868, "jellyfin.home": 8096, "seerr.home": 5055,
		"qbittorrent-staging.home": 18080, "prowlarr-staging.home": 19696,
		"sonarr-staging.home": 18989, "radarr-staging.home": 17878,
		"profilarr-staging.home": 16868, "jellyfin-staging.home": 18096, "seerr-staging.home": 15055,
	}
	if len(media.HTTP.Routers) != len(want) || len(media.HTTP.Services) != len(want) {
		t.Fatalf("Media Stack has %d routers and %d services, want %d each",
			len(media.HTTP.Routers), len(media.HTTP.Services), len(want))
	}
	seen := map[string]bool{}
	for name, router := range media.HTTP.Routers {
		if len(router.EntryPoints) != 1 || router.EntryPoints[0] != "web" {
			t.Errorf("router %s entry points = %v, want [web]", name, router.EntryPoints)
		}
		if !strings.HasPrefix(router.Rule, "Host(`") || !strings.HasSuffix(router.Rule, "`)") {
			t.Errorf("router %s rule = %q", name, router.Rule)
			continue
		}
		host := strings.TrimSuffix(strings.TrimPrefix(router.Rule, "Host(`"), "`)")
		port, exists := want[host]
		if !exists || seen[host] {
			t.Errorf("router %s has unexpected or duplicate hostname %q", name, host)
			continue
		}
		seen[host] = true
		service, exists := media.HTTP.Services[router.Service]
		wantURL := fmt.Sprintf("http://homelab.home:%d", port)
		if !exists || len(service.LoadBalancer.Servers) != 1 || service.LoadBalancer.Servers[0].URL != wantURL {
			t.Errorf("%s targets %+v, want %s", host, service.LoadBalancer.Servers, wantURL)
		}
	}
	for host := range want {
		if !seen[host] {
			t.Errorf("missing router for %s", host)
		}
	}

	var game struct {
		UDP struct {
			Routers  map[string]traefikRouter  `yaml:"routers"`
			Services map[string]traefikService `yaml:"services"`
		} `yaml:"udp"`
	}
	if err := yaml.Unmarshal(readFile(t, filepath.Join(root, "dynamic", "minecraft.yml")), &game); err != nil {
		t.Fatalf("decode Minecraft route: %v", err)
	}
	if len(game.UDP.Routers) != 1 || len(game.UDP.Services) != 1 {
		t.Fatalf("Minecraft has %d UDP routers and %d services, want one each", len(game.UDP.Routers), len(game.UDP.Services))
	}
	router := game.UDP.Routers["minecraft-bedrock"]
	service := game.UDP.Services[router.Service]
	if len(router.EntryPoints) != 1 || router.EntryPoints[0] != "bedrock" ||
		len(service.LoadBalancer.Servers) != 1 || service.LoadBalancer.Servers[0].Address != "pc.home:19134" {
		t.Fatalf("Minecraft route = %+v, service = %+v, want bedrock -> pc.home:19134", router, service)
	}
}

func TestHomeTraefikPublishesHTTPAndBedrockUDP(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "services", "traefik")
	var static struct {
		EntryPoints map[string]struct {
			Address string `yaml:"address"`
		} `yaml:"entryPoints"`
		Providers struct {
			File struct {
				Directory string `yaml:"directory"`
			} `yaml:"file"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(readFile(t, filepath.Join(root, "traefik.yml")), &static); err != nil {
		t.Fatalf("decode Traefik static config: %v", err)
	}
	if static.EntryPoints["web"].Address != ":80" || static.EntryPoints["bedrock"].Address != ":19134/udp" ||
		static.Providers.File.Directory != "/etc/traefik/dynamic" {
		t.Fatalf("Traefik static config does not wire the expected entry points and file provider: %+v", static)
	}
	siteCompose := filepath.Join(repositoryRoot(t), "sites", "home", "traefik", "compose.yaml")
	command := exec.Command("docker", "compose", "-f", siteCompose, "config", "--format", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve home Traefik Compose: %v\n%s", err, output)
	}
	var compose struct {
		Services map[string]struct {
			Image string `json:"image"`
			Ports []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
				Protocol  string `json:"protocol"`
			} `json:"ports"`
			Volumes []struct {
				Source   string `json:"source"`
				Target   string `json:"target"`
				ReadOnly bool   `json:"read_only"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &compose); err != nil {
		t.Fatalf("decode resolved Traefik Compose: %v\n%s", err, output)
	}
	service, exists := compose.Services["traefik"]
	if !exists || !strings.Contains(service.Image, "@sha256:") || len(service.Ports) != 2 ||
		service.Ports[0].Target != 80 || service.Ports[0].Published != "80" || service.Ports[0].Protocol != "tcp" ||
		service.Ports[1].Target != 19134 || service.Ports[1].Published != "19134" || service.Ports[1].Protocol != "udp" {
		t.Fatalf("resolved Traefik Compose ports or image = %+v", service)
	}
	wantVolumes := map[string]string{
		"/etc/traefik/traefik.yml": filepath.Join(root, "traefik.yml"),
		"/etc/traefik/dynamic":     filepath.Join(repositoryRoot(t), "sites", "home", "traefik", "dynamic"),
	}
	for _, volume := range service.Volumes {
		wantSource, exists := wantVolumes[volume.Target]
		if !exists || volume.Source != wantSource || !volume.ReadOnly {
			t.Errorf("unexpected Traefik mount: %+v", volume)
		}
		delete(wantVolumes, volume.Target)
	}
	if len(wantVolumes) != 0 {
		t.Errorf("missing Traefik mounts: %v", wantVolumes)
	}
}
