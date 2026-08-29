package topology

import "github.com/adkulas/homelab/internal/contractmodel"

const unmanagedConfiguration = "Upstream settings absent from this contract are not observed, applied, or repaired by media-stack."

// ConfigurationContract describes the service topology rendered by this package.
func ConfigurationContract() []contractmodel.Service {
	services := make([]contractmodel.Service, 0, len(mandatoryServices))
	for _, definition := range mandatoryServices {
		services = append(services, contractmodel.Service{
			Name:      definition.name,
			Settings:  topologySettings(definition),
			Unmanaged: unmanagedConfiguration,
		})
	}
	return services
}
