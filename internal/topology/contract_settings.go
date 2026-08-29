package topology

import (
	"fmt"

	"github.com/adkulas/homelab/internal/contractmodel"
)

func topologySettings(definition serviceDefinition) []contractmodel.Setting {
	service := definition.name
	settings := []contractmodel.Setting{
		contractmodel.Declared("image", "Pinned container image", "versions.yaml#images."+service, "immutable image digest", nil, "Update the service image digest in versions.yaml.", "render"),
		contractmodel.Declared("timezone", "Container timezone", "media-stack.yaml#spec.defaults.timezone", "IANA timezone", nil, "Change spec.defaults.timezone in media-stack.yaml.", "initialize", "render"),
		contractmodel.Declared("project", "Compose project identity", "media-stack.yaml#spec.environments.<environment>.projectName", "string", nil, "Change the selected Environment projectName in media-stack.yaml.", "render"),
		contractmodel.Derived("configVolume", "Environment-scoped configuration volume", "spec.environments.<environment>.projectName + service identity", "Compose derives a distinct mutable configuration volume for this service.", "render"),
		contractmodel.Fixed("restartPolicy", "Restart policy", "unless-stopped", "Every long-running service restarts unless explicitly stopped.", "render"),
		contractmodel.Fixed("logging", "Bounded logging policy", "json-file; max-size=10m; max-file=3", "The json-file driver keeps three 10 MiB files.", "render"),
		contractmodel.Derived("applicationNetwork", "Environment application network", "spec.environments.<environment>.projectName + application", "Compose scopes the application network to the selected Environment.", "render"),
	}
	if definition.identity != inheritedIdentity {
		settings = append(settings,
			contractmodel.Declared("runtime.uid", "Runtime user identity", "media-stack.yaml#spec.defaults.runtimeUID", "positive integer", nil, "Change spec.defaults.runtimeUID in media-stack.yaml.", "initialize", "render"),
			contractmodel.Declared("runtime.gid", "Runtime group identity", "media-stack.yaml#spec.defaults.runtimeGID", "positive integer", nil, "Change spec.defaults.runtimeGID in media-stack.yaml.", "initialize", "render"),
		)
	}
	if definition.port != nil || service == "qbittorrent" {
		settings = append(settings, contractmodel.Declared("port", "Environment LAN port", servicePortSource(service), "TCP port", nil, "Change the selected Environment service port in media-stack.yaml.", "render", "reconcile"))
	}
	if definition.port != nil {
		settings = append(settings, contractmodel.Declared("lanBindAddress", "LAN bind address", "media-stack.yaml#spec.defaults.lanBindAddress", "IP address", nil, "Change spec.defaults.lanBindAddress in media-stack.yaml.", "render", "reconcile", "verify"))
	}
	if definition.dataMount != nil {
		settings = append(settings, contractmodel.Declared("dataRoot", "Environment data root", "media-stack.yaml#spec.environments.<environment>.dataRoot", "absolute path", nil, "Change the selected Environment dataRoot in media-stack.yaml.", "render", "initialize", "verify", "preserve"))
	}
	if service == "gluetun" {
		settings = append(settings,
			contractmodel.Declared("vpn.provider", "VPN provider", "media-stack.yaml#spec.acquisition.vpn.provider", "string", []string{"nordvpn"}, "Change spec.acquisition.vpn.provider within the supported values.", "render", "initialize", "verify"),
			contractmodel.Declared("vpn.protocol", "VPN protocol", "media-stack.yaml#spec.acquisition.vpn.protocol", "string", []string{"openvpn"}, "Change spec.acquisition.vpn.protocol within the supported values.", "render", "initialize", "verify"),
			contractmodel.Declared("vpn.openvpnProtocol", "OpenVPN transport", "media-stack.yaml#spec.acquisition.vpn.openvpnProtocol", "string", []string{"udp", "tcp"}, "Change spec.acquisition.vpn.openvpnProtocol in media-stack.yaml.", "render", "initialize", "verify"),
			contractmodel.Declared("vpn.server.countries", "VPN server countries", "media-stack.yaml#spec.acquisition.vpn.server.countries", "string list", nil, "Change the semantic country list in media-stack.yaml.", "render", "initialize", "verify"),
			contractmodel.Declared("vpn.server.categories", "VPN server categories", "media-stack.yaml#spec.acquisition.vpn.server.categories", "string list", []string{"P2P"}, "Change the optional semantic category list in media-stack.yaml.", "render", "initialize", "verify"),
			contractmodel.Declared("vpn.catalogueUpdateInterval", "Gluetun catalogue interval", "media-stack.yaml#spec.acquisition.vpn.catalogueUpdateInterval", "duration", nil, "Change the catalogue interval in media-stack.yaml.", "render", "initialize"),
			contractmodel.Secret("vpn.openvpn.username", "OpenVPN service username", "selected Environment SOPS document#openvpn.username", "render", "verify"),
			contractmodel.Secret("vpn.openvpn.password", "OpenVPN service password", "selected Environment SOPS document#openvpn.password", "render", "verify"),
			contractmodel.Fixed("tunDevice", "TUN device contract", "/dev/net/tun", "Gluetun requires the host TUN device.", "render", "verify"),
			contractmodel.Fixed("netAdminCapability", "Network administration capability", "NET_ADMIN", "Gluetun alone receives NET_ADMIN.", "render", "verify"),
			contractmodel.Fixed("firewall.enabled", "Gluetun firewall", true, "The VPN firewall is always enabled.", "render", "verify"),
			contractmodel.Derived("firewall.inputPorts", "Firewall input ports", "spec.environments.<environment>.ports.qbittorrent", "The qBittorrent Web UI port is admitted to the shared namespace.", "render"),
			contractmodel.Fixed("vpn.portForwarding", "VPN port forwarding", false, "VPN port forwarding is disabled.", "render"),
			contractmodel.Derived("networkAliases", "qBittorrent network alias", "application network + qbittorrent identity", "Gluetun owns the qBittorrent alias on the application network.", "render"),
			contractmodel.Derived("qbittorrentPortPublication", "qBittorrent port publication", "spec.defaults.lanBindAddress + spec.environments.<environment>.ports.qbittorrent", "Gluetun publishes the shared qBittorrent Web UI port.", "render"),
		)
	}
	if service == "qbittorrent" {
		settings = append(settings,
			contractmodel.Derived("networkMode", "VPN-shared networking", "service:gluetun", "qBittorrent shares Gluetun's network namespace and has no independent network.", "render"),
			contractmodel.Fixed("startupDependency", "Healthy Gluetun dependency", "service_healthy", "qBittorrent starts only after Gluetun is healthy.", "render"),
			contractmodel.Derived("webUIPort", "Canonical Web UI port", "spec.environments.<environment>.ports.qbittorrent", "The same Environment port is used by qBittorrent and Gluetun publication.", "render"),
		)
	}
	if service == "profilarr" {
		settings = append(settings, contractmodel.Secret("apiKey", "Profilarr API key", "selected Environment SOPS document#profilarr.apiKey", "render", "verify"))
	}
	if service == "jellyfin" {
		settings = append(settings,
			contractmodel.Declared("hardwareTranscoding.preference", "Hardware-transcoding preference", "media-stack.yaml#spec.environments.<environment>.hardwareTranscoding", "string", []string{"auto", "disabled"}, "Change hardwareTranscoding for the selected Environment in media-stack.yaml.", "render", "initialize", "verify"),
			contractmodel.Derived("hardwareTranscoding.device", "Hardware render-device mapping", "detected host render device", "A supported device and group are rendered only when auto detection succeeds.", "render", "verify"),
		)
	}
	settings = append(settings, topologySupplement(definition)...)
	settings = append(settings, contractmodel.Unmanaged())
	for index := range settings {
		if settings[index].Description == "" {
			settings[index].Description = fmt.Sprintf("%s configuration", settings[index].Name)
		}
	}
	return settings
}
