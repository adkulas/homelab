package topology

import (
	"fmt"

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
	if definition.dataMount != nil {
		readOnly := "read-write"
		if definition.name == "jellyfin" {
			readOnly = "read-only"
		}
		settings = append(settings, contractmodel.Derived("dataMount", "Service data mount", "spec.environments.<environment>.dataRoot + service mount policy", "The selected Environment data root is mounted "+readOnly+" at the service's path-identical data seam.", "render"))
	}
	if definition.port != nil && definition.name != "gluetun" {
		settings = append(settings,
			contractmodel.Fixed("port.target", "Container target port", fmt.Sprintf("%d", definition.targetPort), "The upstream service listens on its stable internal port.", "render"),
			contractmodel.Derived("port.publication", "LAN port publication", "spec.defaults.lanBindAddress + "+servicePortSource(definition.name), "Compose publishes the Environment port on the declared LAN bind address.", "render"),
		)
	}
	return settings
}
