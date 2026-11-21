package jellyfin

import (
	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/contractmodel"
)

func ConfigurationContract() []contractmodel.Setting {
	productionScan := contractmodel.Declared("library.refresh.productionScanTime", "Production recovery scan time", "StackInstance#spec.media.defaults.jellyfinRecoveryScanTime", "string", nil, "Set a local time in HH:MM format in the Stack Instance, then run apply for each Environment.", "reconcile")
	productionScan.Default = config.DefaultJellyfinRecoveryScanTime
	productionScan.Description = "Local time for the single daily full-library recovery scan in the Production Environment. The Jellyfin manual scan remains available."
	return []contractmodel.Setting{
		contractmodel.Secret("administrator.username", "Jellyfin administrator username", "selected Stack Instance Environment SOPS document#jellyfin.username", "initialize", "reconcile", "verify"),
		contractmodel.Secret("administrator.password", "Jellyfin administrator password", "selected Stack Instance Environment SOPS document#jellyfin.password", "initialize", "reconcile", "verify"),
		contractmodel.Fixed("startup.locale", "Startup locale", "en-US / US / en", "Jellyfin initializes with the stack's English-US locale policy.", "initialize"),
		contractmodel.Fixed("startup.remoteAccess", "Remote-access policy", "remote access and automatic port mapping disabled", "Jellyfin does not open an independent remote-access path.", "initialize"),
		contractmodel.Fixed("library.movies", "Movie Library definition", movieLibraryPath, "The read-only Movie Library is reconciled at the shared media path.", "reconcile", "verify"),
		contractmodel.Fixed("library.series", "Series Library definition", seriesLibraryPath, "The read-only Series Library is reconciled at the shared media path.", "reconcile", "verify"),
		contractmodel.Fixed("library.refresh.monitoring", "Real-time library monitoring", true, "Jellyfin monitors filesystem changes in both managed libraries and batches affected paths.", "reconcile"),
		contractmodel.Fixed("library.refresh.watchableRoots", "Watchable empty library roots", "media-stack-library.keep", "Apply keeps a non-media marker in each managed library root so the pinned Jellyfin can watch it before any media is present.", "reconcile"),
		contractmodel.Fixed("library.refresh.recoveryTriggers", "Recovery scan triggers", "one daily trigger", "The scheduled scan repairs missed changes and replaces Jellyfin's existing scan triggers. Operators can run the scan manually.", "reconcile"),
		productionScan,
		contractmodel.Fixed("library.refresh.stagingOffsetMinutes", "Staging recovery scan offset in minutes", config.StagingJellyfinRecoveryScanOffsetMinutes, "Staging schedules its own recovery scan after the configured Production wall-clock time, with midnight rollover.", "reconcile"),
		contractmodel.SensitiveDerived("library.refresh.apiKey", "Jellyfin refresh API key", "selected Stack Instance Environment SOPS document#jellyfinRefresh.apiKey", "Jellyfin issues an Environment-specific API key for native Radarr and Sonarr targeted updates. Apply persists it encrypted.", "Revoke the managed key in the selected Jellyfin instance and rerun apply to issue and reconcile its replacement.", "reconcile"),
		contractmodel.Fixed("user.deletionPolicy", "Destructive deletion policy", "deletion disabled for every user", "Content deletion and per-folder deletion are disabled.", "reconcile", "verify"),
	}
}
