package prowlarr

import "github.com/adkulas/homelab/internal/contractmodel"

func ConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.SensitiveDerived("apiKey", "Generated Prowlarr API key", "Prowlarr generated credential", "Prowlarr generates this sensitive API credential; media-stack observes it from Environment-specific mutable state.", "Rotate it through Prowlarr; media-stack observes the replacement on the next reconciliation.", "reconcile"),
		contractmodel.Declared("publicTorrentSources", "Approved Public Torrent Sources", "media-stack.yaml#spec.acquisition.publicTorrentSources[].id", "catalogued source list", []string{"internetarchive"}, "Change the catalogued source IDs in spec.acquisition.publicTorrentSources.", "reconcile"),
		contractmodel.Declared("publicTorrentSources.enabled", "Public Torrent Source enabled state", "media-stack.yaml#spec.acquisition.publicTorrentSources[].enabled", "boolean", nil, "Change enabled for the catalogued source in media-stack.yaml.", "reconcile"),
		contractmodel.Fixed("indexer.internetArchive", "Internet Archive source details", "Internet Archive; definition=internetarchive; protocol=torrent; privacy=public; RSS=true; search=true", "The approved public source uses the pinned Internet Archive Cardigann definition, public torrent protocol, RSS/search, and full source details.", "reconcile"),
		contractmodel.Fixed("application.radarr", "Radarr application link", "Radarr; fullSync; http://radarr:7878", "Prowlarr full-syncs discovery to Radarr at its internal URL.", "reconcile"),
		contractmodel.Fixed("application.sonarr", "Sonarr application link", "Sonarr; fullSync; http://sonarr:8989; standard and anime categories", "Prowlarr full-syncs standard and anime discovery categories to Sonarr at its internal URL.", "reconcile"),
		contractmodel.Fixed("application.syncMode", "Application synchronization mode", "fullSync", "Prowlarr owns full synchronization for both Arr links.", "reconcile"),
		contractmodel.SensitiveDerived("application.radarr.apiKey", "Radarr link API key", "Radarr generated credential", "The generated Radarr API key is observed and passed in memory.", "Rotate it through Radarr; media-stack observes the replacement on the next reconciliation.", "reconcile"),
		contractmodel.SensitiveDerived("application.sonarr.apiKey", "Sonarr link API key", "Sonarr generated credential", "The generated Sonarr API key is observed and passed in memory.", "Rotate it through Sonarr; media-stack observes the replacement on the next reconciliation.", "reconcile"),
	}
}
