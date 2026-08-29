package sonarr

import "github.com/adkulas/homelab/internal/contractmodel"

const seriesPolicySource = "stacks/media/fixtures/profilarr-series-policy.yaml"

func ConfigurationContract() []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Fixed("library.root", "Series Library root", seriesLibraryRoot, "The Series Library uses the shared hardlink-capable path.", "reconcile"),
		contractmodel.Fixed("downloadClient.qbittorrent", "qBittorrent download client", "qbittorrent:Environment port", "Sonarr uses the internal qBittorrent address with completed-download handling.", "reconcile"),
		contractmodel.Fixed("downloadClient.category", "Series acquisition category", "series", "Sonarr submits acquisition to the series category.", "reconcile"),
		contractmodel.SensitiveDerived("apiKey", "Generated Sonarr API key", "Sonarr generated credential", "Sonarr generates this sensitive API credential; media-stack observes it from Environment-specific mutable state.", "Rotate it through Sonarr; media-stack observes the replacement on the next reconciliation.", "reconcile"),
		contractmodel.Secret("downloadClient.credentials", "qBittorrent client credentials", "selected Environment SOPS document#qbittorrent", "reconcile"),
	}
	for _, item := range []struct{ id, name string }{
		{"profile", "Series quality profile"},
		{"profile.customFormats", "Series custom-format scores"},
		{"qualityDefinitions", "Series quality definitions"},
		{"naming.renameEpisodes", "Rename episodes"},
		{"naming.standardEpisodeFormat", "Standard episode format"},
		{"naming.dailyEpisodeFormat", "Daily episode format"},
		{"naming.animeEpisodeFormat", "Anime episode format"},
		{"naming.seriesFolderFormat", "Series folder format"},
		{"naming.seasonFolderFormat", "Season folder format"},
		{"naming.replaceIllegalCharacters", "Illegal-character replacement"},
		{"naming.colonReplacementFormat", "Colon replacement format"},
		{"naming.multiEpisodeStyle", "Multi-episode style"},
		{"mediaManagement", "Series media-management policy"},
	} {
		settings = append(settings, contractmodel.External(item.id, item.name, seriesPolicySource+"#"+item.id, "Profilarr pinned policy"))
	}
	return settings
}
