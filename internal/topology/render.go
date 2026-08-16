package topology

import (
	"fmt"
	"strconv"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

type runtimeIdentity uint8

const (
	inheritedIdentity runtimeIdentity = iota
	puidIdentity
	composeUserIdentity
)

type serviceDefinition struct {
	name         string
	identity     runtimeIdentity
	configTarget string
}

var mandatoryServices = []serviceDefinition{
	{name: "gluetun", identity: inheritedIdentity, configTarget: "/gluetun"},
	{name: "qbittorrent", identity: puidIdentity, configTarget: "/config"},
	{name: "prowlarr", identity: puidIdentity, configTarget: "/config"},
	{name: "sonarr", identity: puidIdentity, configTarget: "/config"},
	{name: "radarr", identity: puidIdentity, configTarget: "/config"},
	{name: "profilarr", identity: puidIdentity, configTarget: "/config"},
	{name: "jellyfin", identity: composeUserIdentity, configTarget: "/config"},
	{name: "seerr", identity: composeUserIdentity, configTarget: "/app/config"},
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
	for _, definition := range mandatoryServices {
		reference, exists := images[definition.name]
		if !exists {
			return nil, fmt.Errorf("checked-in versions do not declare image %q", definition.name)
		}
		if err := config.ValidateImage(definition.name, reference); err != nil {
			return nil, err
		}

		environment := map[string]string{"TZ": defaults.Timezone}
		user := ""
		switch definition.identity {
		case puidIdentity:
			environment["PUID"] = strconv.Itoa(defaults.RuntimeUID)
			environment["PGID"] = strconv.Itoa(defaults.RuntimeGID)
		case composeUserIdentity:
			user = fmt.Sprintf("%d:%d", defaults.RuntimeUID, defaults.RuntimeGID)
		}
		volumeName := definition.name + "-config"
		project.Services[definition.name] = composeService{
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
			Volumes: []string{volumeName + ":" + definition.configTarget},
		}
		project.Volumes[volumeName] = composeVolume{}
	}
	return yaml.Marshal(project)
}
