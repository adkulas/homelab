package qbittorrent

import "github.com/adkulas/homelab/internal/contractmodel"

func ConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Secret("webUI.username", "Stable Web UI username", "selected Environment SOPS document#qbittorrent.username", "initialize", "reconcile", "verify"),
		contractmodel.Secret("webUI.password", "Stable Web UI password", "selected Environment SOPS document#qbittorrent.password", "initialize", "reconcile", "verify"),
		contractmodel.SensitiveDerived("bootstrap.temporaryPassword", "Current-start temporary password", "qBittorrent current-start bootstrap credential", "A fresh service may expose one temporary bootstrap credential; media-stack consumes it in memory and replaces it.", "It cannot be set through Declared Configuration; restart a fresh qBittorrent configuration to regenerate it.", "initialize"),
		contractmodel.Fixed("api.protectedVerification", "Protected API verification", "/api/v2/app/version", "Authentication succeeds only after a protected API observation.", "verify"),
		contractmodel.Fixed("peerAuthentication", "Application-network peer authentication", "pinned disposable peer probe", "Verification proves the declared credentials work from an application-network peer after restart.", "verify"),
		contractmodel.Fixed("savePath", "Default save path", defaultSavePath, "Downloads use the shared hardlink-capable torrent path.", "reconcile"),
		contractmodel.Fixed("automaticTorrentManagement", "Automatic Torrent Management", true, "Automatic Torrent Management and relocation on torrent, save-path, and category change are enabled.", "reconcile"),
		contractmodel.Fixed("category.movies", "Movie acquisition category", "movies", "The movies category saves below /data/torrents/movies.", "reconcile"),
		contractmodel.Fixed("category.series", "Series acquisition category", "series", "The series category saves below /data/torrents/series.", "reconcile"),
		contractmodel.Fixed("seeding.ratioLimit", "Seeding ratio limit", 1.0, "Seeding stops when ratio 1.0 or the time limit is reached.", "reconcile"),
		contractmodel.Fixed("seeding.timeLimit", "Seeding time limit", "seven days", "Seeding stops when seven days or the ratio limit is reached.", "reconcile"),
		contractmodel.Fixed("seeding.limitReachedAction", "Seeding completion action", "stop torrent", "A torrent stops when either seeding limit is reached.", "reconcile"),
	}
}
