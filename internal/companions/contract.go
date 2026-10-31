// Package companions integrates the external series search and queue recovery
// owners. It does not implement either service's scheduler or deletion policy.
package companions

import "github.com/adkulas/homelab/internal/contractmodel"

func HoundarrConfigurationContract() []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Fixed("proactiveSearchOwner", "Proactive missing Series Library search owner", "Houndarr", "Houndarr alone schedules missing episode searches; Sonarr RSS monitoring remains enabled.", "verify"),
		contractmodel.Fixed("search.context", "Sonarr search context", "episode", "Only EpisodeSearch is permitted for proactive missing-content searches.", "verify"),
		contractmodel.Fixed("search.order", "Search order", "chronological", "Original air dates determine oldest-first scanning with persisted page rotation; same-day episodes have no strict order.", "verify"),
		contractmodel.Fixed("search.cutoffEnabled", "Cutoff searches", false, "Cutoff searches are outside this feature.", "verify"),
		contractmodel.Fixed("search.upgradeEnabled", "Library upgrade searches", false, "Completed-library upgrade searches are outside this feature.", "verify"),
		contractmodel.Fixed("search.tagFilters", "Tag inclusion and exclusion", "empty", "No tag filter narrows the monitored missing-episode backlog.", "verify"),
		contractmodel.Fixed("search.hotRetryWindowHours", "Hot retry window", 0, "Hot retries are disabled so the declared cooldown bounds retries.", "verify"),
		contractmodel.Fixed("search.eligibility", "Eligible episodes", "monitored, aired, missing, not queued", "Houndarr evaluates Sonarr wanted episodes and skips queued or ineligible items.", "verify"),
		contractmodel.Fixed("bootstrap", "Configuration mechanism", "guided upstream UI", "Houndarr has no supported configuration API; apply verifies authenticated UI settings and requires guided correction of drift. Databases are never edited.", "verify"),
		contractmodel.Fixed("queueFailureMode", "Unavailable queue inspection", "fail-open", "Upstream backpressure counts Sonarr queue rows once at cycle start and permits searches if queue inspection fails.", "verify"),
		contractmodel.Secret("admin.username", "Houndarr admin username", "selected Environment SOPS document#houndarr.username", "verify"),
		contractmodel.Secret("admin.password", "Houndarr admin password", "selected Environment SOPS document#houndarr.password", "verify"),
		contractmodel.SensitiveDerived("sonarr.apiKey", "Sonarr connection credential", "selected Environment Sonarr /config/config.xml", "The existing Environment Sonarr credential is entered through Houndarr's authenticated UI and encrypted in its state.", "Update in the selected Environment's Houndarr UI after rotating Sonarr's API key.", "verify"),
		contractmodel.Fixed("sonarr.url", "Environment-local Sonarr target", "http://sonarr:8989", "The companion must address Sonarr on its own Compose application network.", "verify"),
	}
	for _, field := range []struct{ id, name, typ string }{
		{"batchSize", "Missing episode batch size", "integer 1..24"}, {"intervalMinutes", "Search cycle interval", "integer 15..1440"}, {"hourlyCap", "Hourly episode search cap", "integer 1..48"}, {"cooldownDays", "Per-episode cooldown", "integer 1..365"}, {"releaseGraceHours", "Post-release grace", "integer 0..72"}, {"allowedWindow", "Allowed search window", "HH:MM-HH:MM in container timezone"}, {"queueLimit", "Sonarr queue backpressure limit", "integer 1..20"},
	} {
		settings = append(settings, policyField("search."+field.id, field.name, field.typ, "verify"))
	}
	return append(settings, policyField("enabled", "Series automation enabled", "boolean", "verify"))
}

func CleanuparrConfigurationContract() []contractmodel.Setting {
	settings := []contractmodel.Setting{
		contractmodel.Fixed("replacementSearchOwner", "Replacement search and queue cleanup owner", "Cleanuparr", "Cleanuparr removes and blocklists bad releases, then dispatches replacements through Sonarr.", "reconcile", "verify"),
		contractmodel.Derived("search.enabled", "Replacement searching", "spec.environments.<environment>.seriesAutomation.enabled", "Global Search remains enabled; replacements dispatch on the two-minute Seeker interval after successful queue removal and blocklisting.", "reconcile", "verify"),
		contractmodel.Fixed("search.proactiveEnabled", "Proactive Seeker", false, "Global and per-instance proactive Seeker are disabled so Houndarr owns proactive searching.", "reconcile", "verify"),
		contractmodel.Fixed("search.replacementScope", "Replacement command context", "single episode: EpisodeSearch; grouped season: SeasonSearch", "Season-pack replacement preserves upstream scope; Houndarr eventually searches still-missing episodes independently.", "verify"),
		contractmodel.Fixed("fullyDeselectedTorrents", "Fully deselected torrent exception", "immediate deletion", "Upstream immediately deletes torrents with all files deselected, bypassing strike thresholds. This is the agreed narrow exception.", "verify"),
		contractmodel.Fixed("recovery.slowOperator", "Slow threshold combination", "OR", "Speed and remaining-time triggers maintain separate strike counters; either can qualify for removal.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.resetOnProgress", "Reset stall and slow strikes on progress", true, "Transient progress resets stall and slow strikes; metadata and failed-import strikes are cumulative upstream.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.failedImportScope", "Failed-import release faults", "No video files were found; Not a valid video file", "Only release-specific import failures qualify; permissions and disk faults require operator repair.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.skipUnavailableClient", "Skip unavailable download client", true, "Missing-client failures do not add failed-import strikes; short peer or VPN outages are tolerated by strike thresholds, not a global outage detector.", "reconcile", "verify"),
		contractmodel.Secret("apiKey", "Cleanuparr API key", "selected Environment SOPS document#cleanuparr.apiKey", "reconcile", "verify"),
		contractmodel.SensitiveDerived("sonarr.apiKey", "Sonarr API key", "selected Environment Sonarr /config/config.xml", "Reconciliation reuses the selected Environment Sonarr credential without rendering it.", "Rotate Sonarr's key and rerun apply.", "reconcile"),
		contractmodel.Secret("qbittorrent.username", "qBittorrent username", "selected Environment SOPS document#qbittorrent.username", "reconcile"),
		contractmodel.Secret("qbittorrent.password", "qBittorrent password", "selected Environment SOPS document#qbittorrent.password", "reconcile"),
		contractmodel.Fixed("sonarr.url", "Sonarr target", "http://sonarr:8989", "Only the selected Compose network's Sonarr is configured; other Arr instances are rejected.", "reconcile", "verify"),
		contractmodel.Derived("qbittorrent.url", "qBittorrent target", "spec.environments.<environment>.ports.qbittorrent", "The internal qbittorrent alias uses the selected Environment port.", "reconcile", "verify"),
		contractmodel.Fixed("search.intervalMinutes", "Replacement dispatch interval", 2, "The shortest supported Seeker interval dispatches queued replacement searches every two minutes plus upstream jitter.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.minimumProgress", "Meaningful stall progress", "1 MiB", "The stalled rule resets on at least one MiB of progress; upstream binary MB units are used.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.failedImportOverride", "Sonarr failed-import threshold source", "global queue policy", "The Sonarr override uses the global declared strike threshold.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.statusChecks", "Service status checks", true, "Check connected service status before queue processing.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.dryRun", "Dry-run cleanup", false, "Enabled automation performs actual cleanup after threshold; disable seriesAutomation to stop it.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.ignoredDownloads", "Ignored-download filters", "empty", "No global or Queue Cleaner ignored-download pattern bypasses the declared recovery policy.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.unknownContent", "Unknown-content cleanup", false, "Queue records without a content identity are not processed.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.errored", "Immediate errored-download policy", false, "Errored-download cleanup is disabled; only declared repeated-observation reasons qualify.", "reconcile", "verify"),
		contractmodel.Fixed("recovery.ruleOwnership", "Stall and slow rule ownership", "one declared rule of each kind", "Apply removes other stall and slow rules because upstream interval validation also considers disabled rules.", "reconcile", "verify"),
		contractmodel.Fixed("otherJobs", "Other cleanup jobs", false, "Download Cleaner, Malware Blocker, and Blacklist Sync are disabled; this feature never removes imported media.", "reconcile", "verify"),
	}
	for _, field := range []struct{ id, name, typ string }{
		{"intervalMinutes", "Queue observation interval", "integer dividing 60 in 5..30"}, {"strikes", "Qualifying observation threshold", "integer 12..288"}, {"minimumSpeedKiB", "Minimum transfer speed", "integer 1..1024 KiB/s"}, {"maximumRemainingHours", "Maximum remaining download time", "integer 24..168 hours"},
	} {
		settings = append(settings, policyField("recovery."+field.id, field.name, field.typ, "reconcile", "verify"))
	}
	return append(settings, policyField("enabled", "Series automation enabled", "boolean", "reconcile", "verify"))
}

func policyField(id, name, typ string, lifecycle ...contractmodel.Lifecycle) contractmodel.Setting {
	return contractmodel.Declared(id, name, "media-stack.yaml#spec.environments.<environment>.seriesAutomation."+id, typ, nil, "Change the selected Environment seriesAutomation policy, rerun apply, and follow any guided Houndarr corrections.", lifecycle...)
}
