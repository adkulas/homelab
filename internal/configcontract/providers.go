package configcontract

import (
	"fmt"
	"sort"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/contractmodel"
)

var validControls = map[contractmodel.Control]bool{
	contractmodel.ControlDeclared: true, contractmodel.ControlSecret: true, contractmodel.ControlDerived: true, contractmodel.ControlFixed: true,
	contractmodel.ControlExternallySynchronized: true, contractmodel.ControlUnmanaged: true,
}

func compose(services []contractmodel.Service) ([]contractmodel.Service, error) {
	for index := range services {
		service := &services[index]
		service.Settings = append(service.Settings, config.ConfigurationContract(service.Name)...)
		registration, exists := serviceRegistry[service.Name]
		if !exists {
			return nil, fmt.Errorf("configuration contract service %q is not registered", service.Name)
		}
		if registration.settings != nil {
			service.Settings = append(service.Settings, registration.settings()...)
		}
		sort.Slice(service.Settings, func(i, j int) bool { return service.Settings[i].ID < service.Settings[j].ID })
		seen := make(map[string]bool, len(service.Settings))
		for _, setting := range service.Settings {
			if setting.ID == "" || setting.Name == "" || setting.Description == "" || setting.Source == "" || setting.Status == "" || setting.OperatorChange == "" || len(setting.Lifecycle) == 0 {
				return nil, fmt.Errorf("configuration contract %s.%s is incomplete", service.Name, setting.ID)
			}
			if seen[setting.ID] {
				return nil, fmt.Errorf("configuration contract %s duplicates setting %q", service.Name, setting.ID)
			}
			seen[setting.ID] = true
			if !validControls[setting.Control] {
				return nil, fmt.Errorf("configuration contract %s.%s has invalid control %q", service.Name, setting.ID, setting.Control)
			}
			if setting.Status != contractmodel.StatusImplemented {
				return nil, fmt.Errorf("configuration contract %s.%s has invalid status %q", service.Name, setting.ID, setting.Status)
			}
			if err := validateLifecycle(service.Name, setting); err != nil {
				return nil, err
			}
			if setting.Control == contractmodel.ControlSecret && !setting.Sensitive {
				return nil, fmt.Errorf("configuration contract secret %s.%s is not sensitive", service.Name, setting.ID)
			}
		}
	}
	return services, nil
}
