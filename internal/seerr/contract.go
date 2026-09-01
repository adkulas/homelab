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
		contractmodel.Fixed("radarrConnection.endpoint", "Internal Radarr connection", "http://radarr:7878", "Movie requests use the selected Environment's application-network Radarr endpoint without TLS or URL base.", "reconcile", "verify"),
		contractmodel.SensitiveDerived("radarrConnection.apiKey", "Radarr API key", "selected Environment Radarr supported API discovery", "Seerr receives the selected Environment's discovered Radarr API key without exposing it.", "Rotate the key in Radarr and rerun apply so Seerr receives the replacement.", "reconcile"),
		contractmodel.Derived("radarrConnection.profile", "Movie request quality profile", "Radarr API profile matching pinned Profilarr Movie Library policy", "Seerr stores the target Environment's numeric Radarr profile identity resolved by policy name.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.directory", "Movie request root", "/data/media/movies", "Approved movie requests enter the shared Movie Library root.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.default", "Default non-4K Radarr destination", true, "The Environment's single Radarr service is the default destination for ordinary movie requests.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.4k", "4K movie requests", false, "This milestone routes ordinary non-4K movie requests only.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.minimumAvailability", "Movie request minimum availability", "released", "Seerr submits this per-item request value without modifying Radarr's service-wide media-management policy.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.search", "Automatic movie search", true, "Approved Seerr movie requests ask Radarr to search automatically.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.scan", "Radarr availability scan", true, "Seerr scans Radarr so request state follows acquisition progress.", "reconcile", "verify"),
		contractmodel.Fixed("radarrConnection.requestTags", "Per-request Radarr tags", false, "Movie requests do not create requester-specific Radarr tags.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.endpoint", "Internal Sonarr connection", "http://sonarr:8989", "Series requests use the selected Environment's application-network Sonarr endpoint without TLS or URL base.", "reconcile", "verify"),
		contractmodel.SensitiveDerived("sonarrConnection.apiKey", "Sonarr API key", "selected Environment Sonarr supported API discovery", "Seerr receives the selected Environment's discovered Sonarr API key without exposing it.", "Rotate the key in Sonarr and rerun apply so Seerr receives the replacement.", "reconcile"),
		contractmodel.Derived("sonarrConnection.profile", "Series request quality profile", "Sonarr API profile matching pinned Profilarr Series Library policy", "Seerr stores the target Environment's numeric Sonarr profile identity resolved by policy name.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.directory", "Series request root", "/data/media/series", "Approved series requests enter the shared Series Library root.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.default", "Default non-4K Sonarr destination", true, "The Environment's single Sonarr service is the default destination for ordinary series requests.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.4k", "4K series requests", false, "This milestone routes ordinary non-4K series requests only.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.seriesType", "Ordinary series type", "standard", "Seerr submits ordinary series with Sonarr's standard series type.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.seasonFolders", "Season folders", true, "Sonarr organizes requested series episodes beneath season folders.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.search", "Automatic series search", true, "Approved Seerr series requests ask Sonarr to search automatically.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.scan", "Sonarr availability scan", true, "Seerr scans Sonarr so request state follows acquisition progress.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.requestTags", "Per-request Sonarr tags", false, "Series requests do not create requester-specific Sonarr tags.", "reconcile", "verify"),
		contractmodel.Fixed("sonarrConnection.monitorNewItems", "New-season monitoring", "all", "Sonarr monitors new seasons for requested series.", "reconcile", "verify"),
	}
}
