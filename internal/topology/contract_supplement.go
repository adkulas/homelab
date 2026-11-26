package topology

import (
	"fmt"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/contractmodel"
)

func topologySupplement(definition serviceDefinition) []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Derived("configMount", "Mutable configuration mount", "configVolume + "+definition.configTarget, "The Environment-scoped configuration volume is mounted at the service's required configuration path.", "render"),
	}
	if definition.identity == puidIdentity {
		settings = append(settings, contractmodel.Fixed("runtime.mode", "Runtime identity mechanism", "PUID and PGID environment", "The image receives the declared numeric identity through LinuxServer-compatible variables.", "render"))
	}
	if definition.identity == composeUserIdentity {
		settings = append(settings, contractmodel.Derived("runtime.mode", "Runtime identity mechanism", "spec.defaults.runtimeUID + spec.defaults.runtimeGID", "Compose runs the service directly as the declared numeric user and group.", "render"))
	}
	if definition.name == "audiobookshelf" {
		settings = append(settings,
			contractmodel.Fixed("setup.network", "First-run network", AudiobookshelfSetupNetwork, "The uninitialized service uses a separate project network until apply creates the household account.", "initialize"),
			contractmodel.Fixed("setup.port.publication", "First-run port publication", "127.0.0.1::80", "During initial account setup Docker publishes a temporary loopback port. Apply then restores the Environment LAN port.", "initialize"),
			contractmodel.Derived("dataMount", "Audiobook library mount", "selected Environment dataRoot + /books/audiobooks", "The operator imports books on the host. Audiobookshelf reads them at /audiobooks.", "initialize", "render", "verify"),
			contractmodel.Derived("metadataVolume", "Audiobookshelf metadata volume", "selected Compose project + audiobookshelf-metadata", "Compose mounts this volume at /metadata. Backup and restore cover it alongside /config.", "render", "preserve"),
		)
	} else if definition.dataMount != nil {
		readOnly := "read-write"
		if definition.name == "jellyfin" {
			readOnly = "read-only"
		}
		settings = append(settings, contractmodel.Derived("dataMount", "Service data mount", "spec.environments.<environment>.dataRoot + service mount policy", "The selected Environment data root is mounted "+readOnly+" at the service's path-identical data seam.", "render"))
	}
	if definition.port != nil && definition.name != "gluetun" {
		source := "spec.defaults.lanBindAddress"
		if config.IsSeriesCompanion(definition.name) {
			source = "Stack Policy#management.bindAddress"
		}
		settings = append(settings,
			contractmodel.Fixed("port.target", "Container target port", fmt.Sprintf("%d", definition.targetPort), "The upstream service listens on its stable internal port.", "render"),
			contractmodel.Derived("port.publication", "LAN port publication", source+" + "+servicePortSource(definition.name), "Compose publishes the Environment port on the declared LAN bind address.", "render"),
		)
	}
	return settings
}
