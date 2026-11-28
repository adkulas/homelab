package shelfarr

import "github.com/adkulas/homelab/internal/contractmodel"

func ConfigurationContract() []contractmodel.Setting {
	token := contractmodel.Secret("hardcover.apiToken", "Optional Hardcover API token", "selected Stack Instance Environment SOPS document#hardcover.apiToken", "reconcile", "verify")
	token.Description = "When supplied, apply sets the shared Discover token through Shelfarr's authenticated settings. If absent, apply leaves the Shelfarr token field alone. Plan observes whether there is a saved token and apply compares its value."
	return []contractmodel.Setting{
		contractmodel.Fixed("household.username", "Initial household account", "household", "Apply creates the initial local administrator account. Additional accounts are managed in Shelfarr.", "initialize"),
		contractmodel.Fixed("household.displayName", "Initial household display name", "Household", "Shelfarr uses this display name when apply creates the household account.", "initialize"),
		contractmodel.SensitiveDerived("household.password", "Household password", "selected Stack Instance Environment SOPS document#jellyfin.password", "Apply uses the existing household password to initialize Shelfarr and sign in to reconcile settings.", "Keep the Shelfarr household password in sync with the selected Environment credential.", "initialize", "reconcile", "verify"),
		contractmodel.Fixed("search.openLibrary", "Open Library search", true, "Title and author search is available without Hardcover.", "reconcile", "verify"),
		contractmodel.Fixed("search.providerMode", "Metadata provider mode", "auto", "Shelfarr searches enabled metadata providers, including Open Library.", "reconcile", "verify"),
		contractmodel.Fixed("requests.immediateSearch", "Start request searches immediately", true, "Shelfarr queues a search when a user submits a request, without an approval step.", "reconcile", "verify"),
		contractmodel.Fixed("hardcover.enabled", "Enable shared Hardcover", true, "Apply enables Hardcover when the selected Environment supplies a token. Without a token, apply preserves the setting.", "reconcile", "verify"),
		token,
	}
}
