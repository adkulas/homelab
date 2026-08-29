package configcontract

import (
	"fmt"

	"github.com/adkulas/homelab/internal/contractmodel"
)

var validLifecycles = map[contractmodel.Lifecycle]bool{
	contractmodel.LifecycleRender:      true,
	contractmodel.LifecycleInitialize:  true,
	contractmodel.LifecycleReconcile:   true,
	contractmodel.LifecycleSynchronize: true,
	contractmodel.LifecycleVerify:      true,
	contractmodel.LifecyclePreserve:    true,
}

func validateLifecycle(service string, setting contractmodel.Setting) error {
	for _, lifecycle := range setting.Lifecycle {
		if !validLifecycles[lifecycle] {
			return fmt.Errorf("configuration contract %s.%s has invalid lifecycle %q", service, setting.ID, lifecycle)
		}
	}
	return nil
}
