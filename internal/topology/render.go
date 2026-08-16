package topology

import (
	"fmt"
	"strconv"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

var mandatoryServices = []string{
	"gluetun",
	"qbittorrent",
	"prowlarr",
	"sonarr",
	"radarr",
	"profilarr",
	"jellyfin",
	"seerr",
}

var puidServices = map[string]bool{
	"qbittorrent": true,
	"prowlarr":    true,
	"sonarr":      true,
	"radarr":      true,
	"profilarr":   true,
}

var composeUserServices = map[string]bool{
	"jellyfin": true,
	"seerr":    true,
}

type composeProject struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]composeVolume  `yaml:"volumes"`
}

type composeService struct {
	Image       string            `yaml:"image"`
	User        string            `yaml:"user,omitempty"`
	Environment map[string]string `yaml:"environment"`
	Restart     string            `yaml:"restart"`
	Logging     composeLogging    `yaml:"logging"`
	Volumes     []string          `yaml:"volumes"`
}

type composeLogging struct {
	Driver  string            `yaml:"driver"`
	Options map[string]string `yaml:"options"`
}

type composeVolume struct{}

func Render(defaults config.Defaults, images map[string]string) ([]byte, error) {
	if defaults.Timezone == "" {
		return nil, fmt.Errorf("declared timezone is required")
	}
	if defaults.RuntimeUID <= 0 || defaults.RuntimeGID <= 0 {
		return nil, fmt.Errorf("declared runtime UID and GID must be non-root numeric IDs")
	}

	project := composeProject{
		Services: make(map[string]composeService, len(mandatoryServices)),
		Volumes:  make(map[string]composeVolume, len(mandatoryServices)),
	}
	for _, name := range mandatoryServices {
		reference, exists := images[name]
		if !exists {
			return nil, fmt.Errorf("checked-in versions do not declare image %q", name)
		}
		if err := config.ValidateImage(name, reference); err != nil {
			return nil, err
		}

		environment := map[string]string{"TZ": defaults.Timezone}
		if puidServices[name] {
			environment["PUID"] = strconv.Itoa(defaults.RuntimeUID)
			environment["PGID"] = strconv.Itoa(defaults.RuntimeGID)
		}
		user := ""
		if composeUserServices[name] {
			user = fmt.Sprintf("%d:%d", defaults.RuntimeUID, defaults.RuntimeGID)
		}
		volumeName := name + "-config"
		volumeTarget := "/config"
		switch name {
		case "gluetun":
			volumeTarget = "/gluetun"
		case "seerr":
			volumeTarget = "/app/config"
		}
		project.Services[name] = composeService{
			Image:       reference,
			User:        user,
			Environment: environment,
			Restart:     "unless-stopped",
			Logging: composeLogging{
				Driver: "json-file",
				Options: map[string]string{
					"max-size": "10m",
					"max-file": "3",
				},
			},
			Volumes: []string{volumeName + ":" + volumeTarget},
		}
		project.Volumes[volumeName] = composeVolume{}
	}
	return yaml.Marshal(project)
}
