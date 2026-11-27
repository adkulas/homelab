package topology

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/hardware"
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
	port         func(config.Ports) int
	targetPort   int
	dataMount    func(string) string
}

var mandatoryServices = []serviceDefinition{
	{name: "calibre-web", identity: puidIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.CalibreWeb }, targetPort: 8083},
	{name: "gluetun", identity: inheritedIdentity, configTarget: "/gluetun", port: func(ports config.Ports) int { return ports.QBittorrent }, targetPort: 8080},
	{name: "qbittorrent", identity: puidIdentity, configTarget: "/config", dataMount: func(root string) string { return path.Join(root, "torrents") + ":/data/torrents" }},
	{name: "prowlarr", identity: puidIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.Prowlarr }, targetPort: 9696},
	{name: "sonarr", identity: puidIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.Sonarr }, targetPort: 8989, dataMount: func(root string) string { return root + ":/data" }},
	{name: "radarr", identity: puidIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.Radarr }, targetPort: 7878, dataMount: func(root string) string { return root + ":/data" }},
	{name: "profilarr", identity: puidIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.Profilarr }, targetPort: 6868},
	{name: "jellyfin", identity: composeUserIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.Jellyfin }, targetPort: 8096, dataMount: func(root string) string { return path.Join(root, "media") + ":/data/media:ro" }},
	{name: "houndarr", identity: puidIdentity, configTarget: "/data", port: func(ports config.Ports) int { return ports.Houndarr }, targetPort: 8877},
	{name: "cleanuparr", identity: puidIdentity, configTarget: "/config", port: func(ports config.Ports) int { return ports.Cleanuparr }, targetPort: 11011},
	{name: "audiobookshelf", configTarget: "/config", port: func(ports config.Ports) int { return ports.Audiobookshelf }, targetPort: 80, dataMount: func(root string) string { return path.Join(root, "books", "audiobooks") + ":/audiobooks:ro" }},
	{name: "seerr", identity: composeUserIdentity, configTarget: "/app/config", port: func(ports config.Ports) int { return ports.Seerr }, targetPort: 5055},
}

type composeProject struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks"`
	Secrets  map[string]composeSecret  `yaml:"secrets"`
	Volumes  map[string]composeVolume  `yaml:"volumes"`
}

type composeService struct {
	Tmpfs       []string                         `yaml:"tmpfs,omitempty"`
	Image       string                           `yaml:"image"`
	User        string                           `yaml:"user,omitempty"`
	Environment map[string]string                `yaml:"environment"`
	EnvFile     []composeEnvFile                 `yaml:"env_file,omitempty"`
	CapAdd      []string                         `yaml:"cap_add,omitempty"`
	Devices     []string                         `yaml:"devices,omitempty"`
	GroupAdd    []string                         `yaml:"group_add,omitempty"`
	Restart     string                           `yaml:"restart"`
	Logging     composeLogging                   `yaml:"logging"`
	NetworkMode string                           `yaml:"network_mode,omitempty"`
	Networks    map[string]composeServiceNetwork `yaml:"networks,omitempty"`
	DependsOn   map[string]composeDependency     `yaml:"depends_on,omitempty"`
	Ports       []string                         `yaml:"ports,omitempty"`
	Secrets     []string                         `yaml:"secrets,omitempty"`
	Volumes     []string                         `yaml:"volumes"`
}

type composeServiceNetwork struct {
	Aliases []string `yaml:"aliases,omitempty"`
}

type composeDependency struct {
	Condition string `yaml:"condition"`
}

type composeEnvFile struct {
	Path     string `yaml:"path"`
	Required bool   `yaml:"required"`
}

type composeLogging struct {
	Driver  string            `yaml:"driver"`
	Options map[string]string `yaml:"options"`
}

type composeVolume struct{}
type composeNetwork struct{}
type composeSecret struct {
	File string `yaml:"file"`
}

func Render(defaults config.Defaults, environment config.Environment, vpn config.VPN, images map[string]string, runtimeSecretDirectory string, transcoding hardware.Transcoding, bootRecovery bool) ([]byte, error) {
	if defaults.Timezone == "" {
		return nil, fmt.Errorf("declared timezone is required")
	}
	if defaults.RuntimeUID <= 0 || defaults.RuntimeGID <= 0 {
		return nil, fmt.Errorf("declared runtime UID and GID must be non-root numeric IDs")
	}

	project := composeProject{
		Name:     environment.ProjectName,
		Services: make(map[string]composeService, len(mandatoryServices)),
		Networks: map[string]composeNetwork{"application": {}},
		Secrets: map[string]composeSecret{
			"openvpn_user":     {File: filepath.Join(runtimeSecretDirectory, "openvpn_user")},
			"openvpn_password": {File: filepath.Join(runtimeSecretDirectory, "openvpn_password")},
		},
		Volumes: make(map[string]composeVolume, len(mandatoryServices)),
	}
	for _, definition := range mandatoryServices {
		if config.IsSeriesCompanion(definition.name) && environment.SeriesAutomation == nil {
			continue
		}
		reference, exists := images[definition.name]
		if !exists {
			return nil, fmt.Errorf("checked-in versions do not declare image %q", definition.name)
		}
		if err := config.ValidateImage(definition.name, reference); err != nil {
			return nil, err
		}

		serviceEnvironment := map[string]string{"TZ": defaults.Timezone}
		user := ""
		switch definition.identity {
		case puidIdentity:
			serviceEnvironment["PUID"] = strconv.Itoa(defaults.RuntimeUID)
			serviceEnvironment["PGID"] = strconv.Itoa(defaults.RuntimeGID)
		case composeUserIdentity:
			user = fmt.Sprintf("%d:%d", defaults.RuntimeUID, defaults.RuntimeGID)
		}
		volumeName := definition.name + "-config"
		mounts := []string{volumeName + ":" + definition.configTarget}
		if definition.name == "audiobookshelf" {
			mounts = append(mounts, "audiobookshelf-metadata:/metadata")
			project.Volumes["audiobookshelf-metadata"] = composeVolume{}
		}
		if definition.name == "calibre-web" {
			mounts = append(mounts, path.Join(environment.DataRoot, "books/calibre")+":/calibre-library", path.Join(environment.DataRoot, "books/ebooks/inbox")+":/cwa-book-ingest")
		}
		if definition.dataMount != nil {
			mounts = append(mounts, definition.dataMount(environment.DataRoot))
		}
		var ports []string
		if definition.port != nil {
			targetPort := definition.targetPort
			if definition.name == "gluetun" {
				targetPort = environment.Ports.QBittorrent
			}
			bindAddress := defaults.LANBindAddress
			if config.IsSeriesCompanion(definition.name) {
				bindAddress = "127.0.0.1"
			}
			ports = []string{fmt.Sprintf("%s:%d:%d", bindAddress, definition.port(environment.Ports), targetPort)}
		}
		var secrets []string
		var envFiles []composeEnvFile
		var capabilities []string
		var devices []string
		var groups []string
		networkMode := ""
		networks := map[string]composeServiceNetwork{"application": {}}
		var dependsOn map[string]composeDependency
		if definition.name == "gluetun" {
			serviceEnvironment["VPN_SERVICE_PROVIDER"] = vpn.Provider
			serviceEnvironment["VPN_TYPE"] = vpn.Protocol
			serviceEnvironment["OPENVPN_PROTOCOL"] = vpn.OpenVPNProtocol
			serviceEnvironment["SERVER_COUNTRIES"] = strings.Join(vpn.Server.Countries, ",")
			if len(vpn.Server.Categories) > 0 {
				serviceEnvironment["SERVER_CATEGORIES"] = strings.Join(vpn.Server.Categories, ",")
			}
			serviceEnvironment["UPDATER_PERIOD"] = vpn.CatalogueUpdateInterval
			serviceEnvironment["FIREWALL"] = "on"
			serviceEnvironment["FIREWALL_INPUT_PORTS"] = strconv.Itoa(environment.Ports.QBittorrent)
			serviceEnvironment["VPN_PORT_FORWARDING"] = "off"
			serviceEnvironment["OPENVPN_USER_SECRETFILE"] = "/run/secrets/openvpn_user"
			serviceEnvironment["OPENVPN_PASSWORD_SECRETFILE"] = "/run/secrets/openvpn_password"
			secrets = []string{"openvpn_user", "openvpn_password"}
			capabilities = []string{"NET_ADMIN"}
			devices = []string{"/dev/net/tun:/dev/net/tun"}
			networks["application"] = composeServiceNetwork{Aliases: []string{"qbittorrent"}}
		}
		if definition.name == "qbittorrent" {
			serviceEnvironment["WEBUI_PORT"] = strconv.Itoa(environment.Ports.QBittorrent)
			networkMode = "service:gluetun"
			networks = nil
			dependsOn = map[string]composeDependency{"gluetun": {Condition: "service_healthy"}}
		}
		if definition.name == "profilarr" {
			envFiles = []composeEnvFile{{Path: filepath.Join(runtimeSecretDirectory, "profilarr.env"), Required: false}}
		}
		if definition.name == "jellyfin" && transcoding.Status == hardware.TranscodingSupported {
			devices = []string{transcoding.RenderDevice + ":" + transcoding.RenderDevice}
			groups = []string{strconv.Itoa(transcoding.GroupID)}
		}
		restart := "unless-stopped"
		if bootRecovery && (definition.name == "gluetun" || definition.name == "qbittorrent") {
			restart = "on-failure"
		}
		var maskedPaths []string
		if definition.name == "sonarr" || definition.name == "radarr" {
			maskedPaths = []string{"/data/books:ro,noexec,nosuid,nodev"}
		}
		project.Services[definition.name] = composeService{
			Tmpfs:       maskedPaths,
			Image:       reference,
			User:        user,
			Environment: serviceEnvironment,
			EnvFile:     envFiles,
			CapAdd:      capabilities,
			Devices:     devices,
			GroupAdd:    groups,
			Restart:     restart,
			Logging: composeLogging{
				Driver: "json-file",
				Options: map[string]string{
					"max-size": "10m",
					"max-file": "3",
				},
			},
			NetworkMode: networkMode,
			Networks:    networks,
			DependsOn:   dependsOn,
			Ports:       ports,
			Secrets:     secrets,
			Volumes:     mounts,
		}
		project.Volumes[volumeName] = composeVolume{}
	}
	return yaml.Marshal(project)
}

const AudiobookshelfSetupNetwork = "audiobookshelf-setup"

// AudiobookshelfSetup restricts the unauthenticated first-run endpoint to a
// temporary loopback port. Compose reuses the same project and application volumes.
func AudiobookshelfSetup(compose []byte) ([]byte, error) {
	var project composeProject
	if err := yaml.Unmarshal(compose, &project); err != nil {
		return nil, err
	}
	service := project.Services["audiobookshelf"]
	project.Networks[AudiobookshelfSetupNetwork] = composeNetwork{}
	service.Networks = map[string]composeServiceNetwork{AudiobookshelfSetupNetwork: {}}
	service.Ports = []string{"127.0.0.1::80"}
	project.Services["audiobookshelf"] = service
	return yaml.Marshal(project)
}
