package config

import (
	"fmt"
	"strconv"
	"strings"
)

// SeriesAutomation is Environment-local acquisition and recovery policy. A nil
// declaration preserves existing installations until companions are configured.
type SeriesAutomation struct {
	Enabled  bool                `yaml:"enabled"`
	Search   EpisodeSearchPolicy `yaml:"search"`
	Recovery QueueRecoveryPolicy `yaml:"recovery"`
}

type EpisodeSearchPolicy struct {
	BatchSize         int    `yaml:"batchSize"`
	IntervalMinutes   int    `yaml:"intervalMinutes"`
	HourlyCap         int    `yaml:"hourlyCap"`
	CooldownDays      int    `yaml:"cooldownDays"`
	ReleaseGraceHours int    `yaml:"releaseGraceHours"`
	AllowedWindow     string `yaml:"allowedWindow"`
	QueueLimit        int    `yaml:"queueLimit"`
}

type QueueRecoveryPolicy struct {
	IntervalMinutes       int `yaml:"intervalMinutes"`
	Strikes               int `yaml:"strikes"`
	MinimumSpeedKiB       int `yaml:"minimumSpeedKiB"`
	MaximumRemainingHours int `yaml:"maximumRemainingHours"`
}

func (policy SeriesAutomation) Validate() error {
	s, r := policy.Search, policy.Recovery
	if s.BatchSize < 1 || s.BatchSize > 24 {
		return fmt.Errorf("seriesAutomation.search.batchSize must be 1..24")
	}
	if s.HourlyCap < 1 || s.HourlyCap > 48 {
		return fmt.Errorf("seriesAutomation.search.hourlyCap must be 1..48")
	}
	if s.IntervalMinutes < 15 || s.IntervalMinutes > 1440 || s.CooldownDays < 1 || s.CooldownDays > 365 || s.ReleaseGraceHours < 0 || s.ReleaseGraceHours > 72 || s.QueueLimit < 1 || s.QueueLimit > 20 {
		return fmt.Errorf("seriesAutomation.search requires intervalMinutes 15..1440, cooldownDays 1..365, releaseGraceHours 0..72 and queueLimit 1..20")
	}
	bounds := strings.Split(s.AllowedWindow, "-")
	validTime := func(value string) bool {
		parts := strings.Split(value, ":")
		if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
			return false
		}
		h, e1 := strconv.Atoi(parts[0])
		m, e2 := strconv.Atoi(parts[1])
		return e1 == nil && e2 == nil && h >= 0 && h < 24 && m >= 0 && m < 60
	}
	if len(bounds) != 2 || !validTime(bounds[0]) || !validTime(bounds[1]) || bounds[0] == bounds[1] {
		return fmt.Errorf("seriesAutomation.search.allowedWindow must be a nonempty HH:MM-HH:MM window")
	}
	if r.IntervalMinutes < 5 || r.IntervalMinutes > 30 || 60%r.IntervalMinutes != 0 || r.Strikes < 12 || r.Strikes > 288 || r.MinimumSpeedKiB < 1 || r.MinimumSpeedKiB > 1024 || r.MaximumRemainingHours < 24 || r.MaximumRemainingHours > 168 {
		return fmt.Errorf("seriesAutomation.recovery requires intervalMinutes dividing 60 in 5..30, strikes 12..288, minimumSpeedKiB 1..1024 and maximumRemainingHours 24..168")
	}
	return nil
}

func validateCompanionPortIsolation(environments map[string]Environment) error {
	type publication struct {
		environment, service string
		port                 int
		companion            bool
	}
	var publications []publication
	for name, environment := range environments {
		ports := environment.Ports
		for service, port := range map[string]int{"qbittorrent": ports.QBittorrent, "prowlarr": ports.Prowlarr, "sonarr": ports.Sonarr, "radarr": ports.Radarr, "profilarr": ports.Profilarr, "jellyfin": ports.Jellyfin, "seerr": ports.Seerr, "houndarr": ports.Houndarr, "cleanuparr": ports.Cleanuparr} {
			companion := IsSeriesCompanion(service)
			if companion && environment.SeriesAutomation == nil {
				continue
			}
			if port > 0 {
				publications = append(publications, publication{name, service, port, companion})
			}
		}
	}
	for index, left := range publications {
		for _, right := range publications[index+1:] {
			if left.port == right.port && (left.companion || right.companion) {
				return fmt.Errorf("companion port collision between %s/%s and %s/%s", left.environment, left.service, right.environment, right.service)
			}
		}
	}
	return nil
}

// IsSeriesCompanion identifies services governed by Environment-local series
// automation, including loopback management and optional legacy inclusion.
func IsSeriesCompanion(service string) bool { return service == "houndarr" || service == "cleanuparr" }
