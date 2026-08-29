package jellyfin

import "github.com/adkulas/homelab/internal/contractmodel"

func ConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Secret("administrator.username", "Jellyfin administrator username", "selected Environment SOPS document#jellyfin.username", "initialize", "reconcile", "verify"),
		contractmodel.Secret("administrator.password", "Jellyfin administrator password", "selected Environment SOPS document#jellyfin.password", "initialize", "reconcile", "verify"),
		contractmodel.Fixed("startup.locale", "Startup locale", "en-US / US / en", "Jellyfin initializes with the stack's English-US locale policy.", "initialize"),
		contractmodel.Fixed("startup.remoteAccess", "Remote-access policy", "remote access and automatic port mapping disabled", "Jellyfin does not open an independent remote-access path.", "initialize"),
		contractmodel.Fixed("library.movies", "Movie Library definition", movieLibraryPath, "The read-only Movie Library is reconciled at the shared media path.", "reconcile", "verify"),
		contractmodel.Fixed("library.series", "Series Library definition", seriesLibraryPath, "The read-only Series Library is reconciled at the shared media path.", "reconcile", "verify"),
		contractmodel.Fixed("user.deletionPolicy", "Destructive deletion policy", "deletion disabled for every user", "Content deletion and per-folder deletion are disabled.", "reconcile", "verify"),
	}
}
