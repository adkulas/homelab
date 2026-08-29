package configcontract

import (
	"fmt"
	"strings"

	"github.com/adkulas/homelab/internal/contractmodel"
)

var humanGroups = []struct {
	control contractmodel.Control
	heading string
}{
	{contractmodel.ControlDeclared, "DECLARED"},
	{contractmodel.ControlSecret, "SECRETS"},
	{contractmodel.ControlExternallySynchronized, "EXTERNALLY SYNCHRONIZED"},
	{contractmodel.ControlDerived, "DERIVED"},
	{contractmodel.ControlFixed, "FIXED"},
	{contractmodel.ControlUnmanaged, "UNMANAGED"},
}

func Human(document contractmodel.Document) string {
	var output strings.Builder
	for serviceIndex, service := range document.Services {
		if serviceIndex > 0 {
			output.WriteString("\n")
		}
		fmt.Fprintf(&output, "%s\n", serviceTitle(service.Name))
		for _, group := range humanGroups {
			fmt.Fprintf(&output, "  %s\n", group.heading)
			for _, setting := range service.Settings {
				if setting.Control != group.control {
					continue
				}
				fmt.Fprintf(&output, "    %s — %s\n", setting.ID, setting.Name)
				fmt.Fprintf(&output, "      control: %s\n", setting.Control)
				fmt.Fprintf(&output, "      source: %s\n", setting.Source)
				if setting.Owner != "" {
					fmt.Fprintf(&output, "      owner: %s\n", setting.Owner)
				}
				if setting.Type != "" {
					fmt.Fprintf(&output, "      type: %s\n", setting.Type)
				}
				if len(setting.AllowedValues) > 0 {
					fmt.Fprintf(&output, "      allowed values: %s\n", strings.Join(setting.AllowedValues, ", "))
				}
				if setting.Default != nil {
					fmt.Fprintf(&output, "      default: %v\n", setting.Default)
				}
				fmt.Fprintf(&output, "      sensitive: %t\n", setting.Sensitive)
				fmt.Fprintf(&output, "      lifecycle: %s\n", joinLifecycles(setting.Lifecycle))
				fmt.Fprintf(&output, "      status: %s\n", setting.Status)
				fmt.Fprintf(&output, "      operator change: %s\n", setting.OperatorChange)
			}
			if group.control == contractmodel.ControlUnmanaged {
				fmt.Fprintf(&output, "    %s\n", service.Unmanaged)
			}
		}
	}
	return output.String()
}

func serviceTitle(name string) string {
	if registration, exists := serviceRegistry[name]; exists {
		return registration.title
	}
	return name
}
