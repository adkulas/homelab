package radarr

import "github.com/adkulas/homelab/internal/contractmodel"

const moviePolicySource = "stacks/media/fixtures/profilarr-movie-policy.yaml"

func ConfigurationContract() []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Fixed("library.root", "Movie Library root", movieLibraryRoot, "The Movie Library uses the shared hardlink-capable path.", "reconcile"),
		contractmodel.Fixed("downloadClient.qbittorrent", "qBittorrent download client", "qBittorrent; host=qbittorrent; Environment port; category=movies", "Radarr uses the internal qBittorrent address with completed-download handling.", "reconcile"),
		contractmodel.Fixed("downloadClient.category", "Movie acquisition category", "movies", "Radarr submits acquisition to the movies category.", "reconcile"),
		contractmodel.SensitiveDerived("apiKey", "Generated Radarr API key", "Radarr generated credential", "Radarr generates this sensitive API credential; media-stack observes it from Environment-specific mutable state.", "Rotate it through Radarr; media-stack observes the replacement on the next reconciliation.", "reconcile"),
		contractmodel.Secret("downloadClient.credentials", "qBittorrent client credentials", "selected Environment SOPS document#qbittorrent", "reconcile"),
	}
	for _, item := range []struct{ id, name string }{
		{"profile", "Movie quality profile"},
		{"profile.customFormats", "Movie custom-format scores"},
		{"qualityDefinitions", "Movie quality definitions"},
		{"naming.renameMovies", "Rename movies"},
		{"naming.standardMovieFormat", "Standard movie format"},
		{"naming.movieFolderFormat", "Movie folder format"},
		{"naming.replaceIllegalCharacters", "Illegal-character replacement"},
		{"naming.colonReplacementFormat", "Colon replacement format"},
		{"mediaManagement", "Movie media-management policy"},
	} {
		settings = append(settings, contractmodel.External(item.id, item.name, moviePolicySource+"#"+item.id, "Profilarr pinned policy"))
	}
	return settings
}
