package config

import "github.com/adkulas/homelab/internal/contractmodel"

// ConfigurationContract returns document-level settings consumed across service lifecycles.
func ConfigurationContract(service string) []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Fixed("document.mediaStackAPIVersion", "Declared Configuration API version", "homelab.media-stack/v1alpha1", "The CLI accepts homelab.media-stack/v1alpha1.", "initialize", "render"),
		contractmodel.Fixed("document.mediaStackKind", "Declared Configuration kind", "MediaStack", "The CLI accepts the MediaStack document kind.", "initialize", "render"),
		contractmodel.Fixed("document.versionsAPIVersion", "Versions API version", "homelab.media-stack/v1alpha1", "The CLI accepts homelab.media-stack/v1alpha1.", "render"),
		contractmodel.Fixed("document.versionsKind", "Versions document kind", "MediaStackVersions", "The CLI accepts the MediaStackVersions document kind.", "render"),
		contractmodel.Declared("backup.root", "Environment backup root", "StackInstance#spec.media.environments.<environment>.backupRoot", "absolute path", nil, "Change the selected Environment backupRoot in the selected Stack Instance.", "preserve"),
		contractmodel.Declared("backup.retention.daily", "Daily backup retention", "StackInstance#spec.media.defaults.backupRetention.daily", "non-negative integer", nil, "Change spec.media.defaults.backupRetention.daily in the selected Stack Instance.", "preserve"),
		contractmodel.Declared("backup.retention.weekly", "Weekly backup retention", "StackInstance#spec.media.defaults.backupRetention.weekly", "non-negative integer", nil, "Change spec.media.defaults.backupRetention.weekly in the selected Stack Instance.", "preserve"),
		contractmodel.Declared("backup.retention.monthly", "Monthly backup retention", "StackInstance#spec.media.defaults.backupRetention.monthly", "non-negative integer", nil, "Change spec.media.defaults.backupRetention.monthly in the selected Stack Instance.", "preserve"),
	}
	if service == "gluetun" || service == "qbittorrent" || service == "profilarr" || service == "jellyfin" || service == "seerr" || IsSeriesCompanion(service) {
		settings = append(settings, contractmodel.Declared("secrets.document", "Environment secret document", "StackInstance#spec.media.environments.<environment>.secretsFile", "path", nil, "Change the selected Environment secretsFile path in the selected Stack Instance.", "initialize", "reconcile", "verify"))
	}
	if service == "gluetun" {
		settings = append(settings, contractmodel.Declared("vpn.secrets.document", "Site VPN secret document", "StackInstance#spec.media.vpnSecretsFile", "Site-relative path", nil, "Reference the separate Site-owned NordVPN SOPS document in the selected Stack Instance.", "initialize", "render", "verify"))
	}
	return settings
}
