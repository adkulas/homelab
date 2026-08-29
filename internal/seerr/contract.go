package seerr

import "github.com/adkulas/homelab/internal/contractmodel"

func ConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Secret("jellyfinAuthentication.credentials", "Household Jellyfin credentials", "selected Environment SOPS document#jellyfin", "initialize", "reconcile", "verify"),
		contractmodel.Fixed("jellyfinAuthentication.enabled", "Jellyfin sign-in", "enabled", "Existing and new household Jellyfin sign-ins are enabled.", "reconcile", "verify"),
		contractmodel.Fixed("localAuthentication.enabled", "Emergency local sign-in", "enabled", "Local authentication remains enabled as an emergency path.", "reconcile", "verify"),
		contractmodel.Secret("localAdministrator.password", "Emergency local administrator password", "selected Environment SOPS document#jellyfin.password", "initialize", "reconcile", "verify"),
		contractmodel.Fixed("localAdministrator.permission", "Emergency local administrator permission", "administrator permission bit", "The emergency local account is verified as an administrator.", "verify"),
		contractmodel.Fixed("defaultRequestPermission", "New household request permission", "request-only", "New Jellyfin-authenticated users receive request permission without administration.", "reconcile"),
		contractmodel.Fixed("jellyfinConnection", "Internal Jellyfin connection", "http://jellyfin:8096", "Seerr bootstraps against the application-network Jellyfin endpoint without TLS or URL base.", "initialize"),
		contractmodel.Fixed("initialization", "Initialization state", "complete after authentication policy", "Seerr initialization completes only after the authentication contract is established.", "initialize"),
	}
}
