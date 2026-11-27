package audiobookshelf

import "github.com/adkulas/homelab/internal/contractmodel"

func ConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Secret("administrator.username", "Initial local administrator username", "selected Stack Instance Environment SOPS document#jellyfin.username", "initialize", "reconcile"),
		contractmodel.Secret("administrator.password", "Initial local administrator password", "selected Stack Instance Environment SOPS document#jellyfin.password", "initialize", "reconcile"),
		contractmodel.Fixed("library.audiobooks", "Audiobooks library", "/audiobooks", "Apply provisions one book library. Additional accounts and their listening positions belong to Audiobookshelf.", "reconcile", "preserve"),
	}
}
