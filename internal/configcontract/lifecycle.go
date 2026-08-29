package configcontract

import (
	"strings"

	"github.com/adkulas/homelab/internal/contractmodel"
)

func joinLifecycles(values []contractmodel.Lifecycle) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = string(value)
	}
	return strings.Join(parts, ", ")
}
