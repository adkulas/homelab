package engine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/companions"
	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"gopkg.in/yaml.v3"
)

type recoveryFixture struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Legal        bool      `yaml:"legalFixture"`
		DownloadID   string    `yaml:"downloadId"`
		SeriesID     int       `yaml:"seriesId"`
		SeasonNumber int       `yaml:"seasonNumber"`
		EpisodeIDs   []int     `yaml:"episodeIds"`
		Context      string    `yaml:"replacementContext"`
		Reason       string    `yaml:"reason"`
		After        time.Time `yaml:"observedAfter"`
		Timeout      string    `yaml:"timeout"`
		Expect       string    `yaml:"expect"`
	} `yaml:"spec"`
}

func loadRecoveryFixture(path string) (recoveryFixture, time.Duration, error) {
	var fixture recoveryFixture
	file, err := os.Open(path)
	if err != nil {
		return fixture, 0, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return fixture, 0, fmt.Errorf("invalid recovery fixture document")
	}
	s := fixture.Spec
	if fixture.APIVersion != "homelab.media-stack/series-recovery/v1alpha1" || fixture.Kind != "SeriesRecoveryFixture" || !s.Legal || s.DownloadID == "" || s.SeriesID <= 0 || len(s.EpisodeIDs) == 0 || s.SeasonNumber < 0 || s.After.IsZero() || s.After.After(time.Now()) || time.Since(s.After) > 24*time.Hour {
		return fixture, 0, fmt.Errorf("recovery fixture requires the supported schema, legalFixture=true, downloadId, seriesId, seasonNumber, episodeIds and observedAfter within the last 24 hours")
	}
	if s.Context != "episode" && s.Context != "season" {
		return fixture, 0, fmt.Errorf("replacementContext must be episode or season")
	}
	if s.Expect != "retained" && s.Expect != "recovered" {
		return fixture, 0, fmt.Errorf("expect must be retained or recovered")
	}
	switch s.Reason {
	case "stalled", "metadata", "slow-speed", "slow-time", "failed-import", "connectivity":
	default:
		return fixture, 0, fmt.Errorf("unknown recovery reason")
	}
	seen := map[int]bool{}
	for _, id := range s.EpisodeIDs {
		if id <= 0 || seen[id] {
			return fixture, 0, fmt.Errorf("episodeIds must be distinct positive IDs")
		}
		seen[id] = true
	}
	timeout, err := time.ParseDuration(s.Timeout)
	if err != nil || timeout <= 0 || timeout > 2*time.Hour {
		return fixture, 0, fmt.Errorf("recovery timeout must be positive and at most two hours")
	}
	return fixture, timeout, nil
}

func verifySeriesRecovery(ctx context.Context, environment config.Environment, secrets environmentSecrets, sonarrURL, sonarrKey, fixturePath string, report *VerifyReport) {
	fixture, timeout, err := loadRecoveryFixture(fixturePath)
	if err != nil {
		report.add("VERIFY_RECOVERY_FIXTURE_INVALID", "Disposable series recovery fixture", err.Error(), false)
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	cleanup := companions.NewCleanuparr(companionAddress(environment.Ports.Cleanuparr), secrets.CleanuparrKey, client)
	observer := companions.NewSonarrEvidence(sonarrURL, sonarrKey, client)
	qbit := qbittorrent.New("http://"+environmentAddress("127.0.0.1", environment.Ports.QBittorrent), client)
	if err := qbit.Login(ctx, secrets.QBittorrent.Username, secrets.QBittorrent.Password); err != nil {
		report.add("VERIFY_COMPANION_TARGET_CREDENTIALS_INVALID", "Recovery qBittorrent authentication", "Correct the selected Environment credential.", false)
		return
	}
	deadline := time.Now().Add(timeout)
	reasons := map[string]string{"stalled": "StalledStrike", "metadata": "DownloadingMetadataStrike", "slow-speed": "SlowSpeedStrike", "slow-time": "SlowTimeStrike", "failed-import": "FailedImportStrike"}
	maxStrikes := 0
	for {
		events, err := cleanup.ObserveQueue(ctx, fixture.Spec.After)
		if err != nil {
			addCompanionFailure(report, err)
			return
		}
		removed := false
		var removedAt time.Time
		for _, event := range events {
			if event.IsDryRun || event.Timestamp.Before(fixture.Spec.After) || !strings.EqualFold(event.ItemHash, fixture.Spec.DownloadID) {
				continue
			}
			if event.EventType == reasons[fixture.Spec.Reason] && event.StrikeCount > maxStrikes {
				maxStrikes = event.StrikeCount
			}
			if event.EventType == "QueueItemDeleted" && event.RemoveFromClient {
				removed = true
				if event.Timestamp.After(removedAt) {
					removedAt = event.Timestamp
				}
			}
		}
		queued, blocked, err := observer.DownloadState(ctx, fixture.Spec.DownloadID)
		if err != nil {
			addCompanionFailure(report, err)
			return
		}
		present, err := qbit.ContainsTorrent(ctx, fixture.Spec.DownloadID)
		if err != nil {
			report.add("VERIFY_COMPANION_TARGET_UNAVAILABLE", "Recovery qBittorrent observation", "Restore qBittorrent API access; no recovery evidence can be inferred from its absence.", false)
			return
		}
		commands, err := observer.Commands(ctx)
		if err != nil {
			addCompanionFailure(report, err)
			return
		}
		after := fixture.Spec.After
		if removedAt.After(after) {
			after = removedAt
		}
		episodeReplacement, seasonReplacement := recoveryCommands(commands, fixture, after)
		relatedSearch := hasRelatedRecoverySearch(commands, fixture)
		if removed && !blocked {
			report.add("VERIFY_BLOCKLIST_FAILED", "Removed release is absent from Sonarr's persisted blocklist", "Stop replacement activity and inspect Sonarr and Cleanuparr errors.", false)
			return
		}
		if fixture.Spec.Expect == "retained" {
			if !queued || !present || blocked || removed || relatedSearch {
				report.add("VERIFY_PREMATURE_CLEANUP", "Below-threshold or transient-outage fixture changed queue, blocklist or replacement state", "Inspect strike policy and upstream cleanup exceptions.", false)
				return
			}
			if maxStrikes >= environment.SeriesAutomation.Recovery.Strikes {
				report.add("VERIFY_CLEANUP_FAILED", "Fixture reached cleanup threshold while retention was expected", "Use a fresh fixture with fewer qualifying observations.", false)
				return
			}
			if time.Now().After(deadline) {
				report.add("VERIFY_TRANSIENT_DOWNLOAD_RETAINED", fmt.Sprintf("Queue and torrent retained with %d qualifying observations; no blocklist or replacement", maxStrikes), "", true)
				return
			}
		} else if removed && blocked && !queued && !present && maxStrikes >= environment.SeriesAutomation.Recovery.Strikes {
			correct := fixture.Spec.Context == "episode" && episodeReplacement || fixture.Spec.Context == "season" && seasonReplacement && episodeReplacement
			if correct {
				report.add("VERIFY_RELEASE_BLOCKLISTED", "Removed release persisted in Sonarr blocklist", "", true)
				report.add("VERIFY_DOWNLOAD_REMOVED", "Rejected download absent from Sonarr queue and qBittorrent", "", true)
				if fixture.Spec.Context == "season" {
					report.add("VERIFY_SEASON_REPLACEMENT_DISPATCHED", "SeasonSearch replacement followed by eventual per-episode fallback", "", true)
				} else {
					report.add("VERIFY_EPISODE_REPLACEMENT_DISPATCHED", "Replacement EpisodeSearch observed for the expected episode IDs", "", true)
				}
				report.add("VERIFY_SERIES_RECOVERY_PROVEN", "Declared reason, threshold, deletion, blocklist and replacement verified", "", true)
				return
			}
		}
		if time.Now().After(deadline) {
			code, remedy := "VERIFY_RECOVERY_TIMEOUT", "Inspect companion thresholds, schedule and service connectivity; fixture recovery did not complete."
			if maxStrikes >= environment.SeriesAutomation.Recovery.Strikes && !blocked {
				code, remedy = "VERIFY_BLOCKLIST_FAILED", "The declared threshold was reached without a persisted removal/blocklist result; inspect the Sonarr cleanup request and service health before retries."
				if !relatedSearch {
					report.add("VERIFY_REPLACEMENT_WITHHELD", "No replacement command observed while blocklisting was unsuccessful", "", true)
				}
			}
			if removed && blocked {
				code, remedy = "VERIFY_REPLACEMENT_FAILED", "Release was removed and blocklisted but the expected Sonarr replacement command or episode fallback was not observed."
			}
			report.add(code, fmt.Sprintf("Recovery reason %s; observed strikes %d", fixture.Spec.Reason, maxStrikes), remedy, false)
			return
		}
		if !waitForMediaPoll(ctx, deadline) {
			if ctx.Err() != nil {
				report.add("VERIFY_RECOVERY_TIMEOUT", "Recovery observation interrupted", "Retry the disposable Staging fixture.", false)
				return
			}
		}
	}
}

// recoveryCommands requires replacements after the cleanup event. A season
// fixture additionally requires every fallback episode after its SeasonSearch.
func recoveryCommands(commands []companions.SearchCommand, fixture recoveryFixture, after time.Time) (episode, season bool) {
	var seasonAt time.Time
	for _, command := range commands {
		at := command.SubmittedAt()
		if !at.After(after) {
			continue
		}
		if command.CommandName() == "SeasonSearch" && command.Body.SeriesID == fixture.Spec.SeriesID && command.Body.SeasonNumber == fixture.Spec.SeasonNumber {
			if seasonAt.IsZero() || at.Before(seasonAt) {
				seasonAt = at
			}
		}
	}
	season = !seasonAt.IsZero()
	episodeAfter := after
	if fixture.Spec.Context == "season" {
		if !season {
			return false, false
		}
		episodeAfter = seasonAt
	}
	searched := map[int]bool{}
	for _, command := range commands {
		if command.CommandName() != "EpisodeSearch" || !command.SubmittedAt().After(episodeAfter) {
			continue
		}
		for _, id := range command.Body.EpisodeIDs {
			searched[id] = true
		}
	}
	episode = true
	for _, id := range fixture.Spec.EpisodeIDs {
		episode = episode && searched[id]
	}
	return episode, season
}

func hasRelatedRecoverySearch(commands []companions.SearchCommand, fixture recoveryFixture) bool {
	expected := map[int]bool{}
	for _, id := range fixture.Spec.EpisodeIDs {
		expected[id] = true
	}
	for _, command := range commands {
		if !command.SubmittedAt().After(fixture.Spec.After) {
			continue
		}
		if command.CommandName() == "SeasonSearch" && command.Body.SeriesID == fixture.Spec.SeriesID && command.Body.SeasonNumber == fixture.Spec.SeasonNumber {
			return true
		}
		if command.CommandName() == "EpisodeSearch" {
			for _, id := range command.Body.EpisodeIDs {
				if expected[id] {
					return true
				}
			}
		}
	}
	return false
}
