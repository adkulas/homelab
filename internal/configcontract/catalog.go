package configcontract

import (
	"fmt"
	"sort"
	"strings"

	"github.com/adkulas/homelab/internal/contractmodel"
	"github.com/adkulas/homelab/internal/topology"
)

func Describe(serviceName string) (contractmodel.Document, error) {
	services, err := compose(topology.ConfigurationContract())
	if err != nil {
		return contractmodel.Document{}, err
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	if serviceName != "" {
		for _, service := range services {
			if service.Name == serviceName {
				return contractmodel.Document{SchemaVersion: contractmodel.SchemaVersion, Services: []contractmodel.Service{service}}, nil
			}
		}
		var names []string
		for _, svc := range services {
			names = append(names, svc.Name)
		}
		return contractmodel.Document{}, fmt.Errorf("unknown service %q; supported services: %s", serviceName, strings.Join(names, ", "))
	}
	return contractmodel.Document{SchemaVersion: contractmodel.SchemaVersion, Services: services}, nil
}
