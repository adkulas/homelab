package config

import "github.com/adkulas/homelab/internal/contractmodel"

// ConfigurationContract returns document-level settings consumed across service lifecycles.
func ConfigurationContract(service string) []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Fixed("document.mediaStackAPIVersion", "Declared Configuration API version", "homelab.media-stack/v1alpha1", "The CLI accepts homelab.media-stack/v1alpha1.", "initialize", "render"),
		contractmodel.Fixed("document.mediaStackKind", "Declared Configuration kind", "MediaStack", "The CLI accepts the MediaStack document kind.", "initialize", "render"),
		contractmodel.Fixed("document.versionsAPIVersion", "Versions API version", "homelab.media-stack/v1alpha1", "The CLI accepts homelab.media-stack/v1alpha1.", "render"),
		contractmodel.Fixed("document.versionsKind", "Versions document kind", "MediaStackVersions", "The CLI accepts the MediaStackVersions document kind.", "render"),
		contractmodel.Declared("backup.root", "Environment backup root", "media-stack.yaml#spec.environments.<environment>.backupRoot", "absolute path", nil, "Change the selected Environment backupRoot in media-stack.yaml.", "preserve"),
		contractmodel.Declared("backup.retention.daily", "Daily backup retention", "media-stack.yaml#spec.defaults.backupRetention.daily", "non-negative integer", nil, "Change spec.defaults.backupRetention.daily in media-stack.yaml.", "preserve"),
		contractmodel.Declared("backup.retention.weekly", "Weekly backup retention", "media-stack.yaml#spec.defaults.backupRetention.weekly", "non-negative integer", nil, "Change spec.defaults.backupRetention.weekly in media-stack.yaml.", "preserve"),
		contractmodel.Declared("backup.retention.monthly", "Monthly backup retention", "media-stack.yaml#spec.defaults.backupRetention.monthly", "non-negative integer", nil, "Change spec.defaults.backupRetention.monthly in media-stack.yaml.", "preserve"),
	}
	if service == "gluetun" || service == "qbittorrent" || service == "profilarr" || service == "jellyfin" || service == "seerr" {
		settings = append(settings, contractmodel.Declared("secrets.document", "Environment secret document", "media-stack.yaml#spec.environments.<environment>.secretsFile", "path", nil, "Change the selected Environment secretsFile path in media-stack.yaml.", "initialize", "reconcile", "verify"))
	}
	return settings
}
