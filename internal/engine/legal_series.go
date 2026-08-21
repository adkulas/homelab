package engine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/sonarr"
	"gopkg.in/yaml.v3"
)

const legalSeriesSchemaVersion = "homelab.media-stack/legal-series/v1alpha1"

type legalSeriesFixture struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Title         string `yaml:"title"`
		TVDBID        int    `yaml:"tvdbId"`
		SeasonNumber  int    `yaml:"seasonNumber"`
		EpisodeNumber int    `yaml:"episodeNumber"`
		ReleaseTitle  string `yaml:"releaseTitle"`
		Indexer       string `yaml:"indexer"`
		Timeout       string `yaml:"timeout"`
	} `yaml:"spec"`
}

func loadLegalSeriesFixture(path string) (legalSeriesFixture, time.Duration, error) {
	var fixture legalSeriesFixture
	file, err := os.Open(path)
	if err != nil {
		return fixture, 0, fmt.Errorf("open legal series fixture: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return fixture, 0, fmt.Errorf("decode legal series fixture: %w", err)
	}
	if fixture.APIVersion != legalSeriesSchemaVersion || fixture.Kind != "LegalSeriesFixture" {
		return fixture, 0, fmt.Errorf("unsupported legal series fixture %q %q", fixture.APIVersion, fixture.Kind)
	}
	if fixture.Spec.Title == "" || fixture.Spec.TVDBID <= 0 || fixture.Spec.SeasonNumber < 0 || fixture.Spec.EpisodeNumber <= 0 || fixture.Spec.ReleaseTitle == "" || fixture.Spec.Indexer != "Internet Archive" {
		return fixture, 0, fmt.Errorf("legal series fixture requires title, positive tvdbId, non-negative seasonNumber, positive episodeNumber, releaseTitle, and approved Internet Archive indexer")
	}
	timeout, err := time.ParseDuration(fixture.Spec.Timeout)
	if err != nil || timeout <= 0 {
		return fixture, 0, fmt.Errorf("legal series fixture timeout must be a positive duration")
	}
	return fixture, timeout, nil
}

func verifyLegalSeries(ctx context.Context, plan Plan, declared config.MediaStack, request VerifyRequest, report *VerifyReport) {
	fixture, timeout, err := loadLegalSeriesFixture(request.legalSeriesFixturePath)
	if err != nil {
		report.add("VERIFY_LEGAL_SERIES_FIXTURE_INVALID", "legal series fixture", err.Error(), false)
		return
	}
	environment := declared.Spec.Environments[request.plan.environment]
	password, err := waitForTemporaryQBittorrentPassword(ctx, plan, 120*time.Second)
	if err != nil {
		report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", err.Error(), false)
		return
	}
	qbClient := qbittorrent.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.QBittorrent), &http.Client{Timeout: 10 * time.Second})
	if err := qbClient.Login(ctx, "admin", password); err != nil {
		report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", err.Error(), false)
		return
	}
	apiKey, err := waitForServiceAPIKey(ctx, plan, "sonarr", "Sonarr", 120*time.Second)
	if err != nil {
		report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", err.Error(), false)
		return
	}
	sonarrClient := sonarr.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Sonarr), apiKey, &http.Client{Timeout: 10 * time.Second})
	seriesID, _, err := sonarrClient.AcquireLegalEpisode(ctx, fixture.Spec.TVDBID, fixture.Spec.SeasonNumber, fixture.Spec.EpisodeNumber, fixture.Spec.ReleaseTitle, fixture.Spec.Indexer)
	if err != nil {
		report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", err.Error(), false)
		return
	}

	deadline := time.Now().Add(timeout)
	var torrent qbittorrent.Torrent
	var torrentFiles []qbittorrent.TorrentFile
	for {
		var complete bool
		torrent, torrentFiles, complete, err = qbClient.CompletedSeries(ctx, fixture.Spec.ReleaseTitle)
		if err != nil {
			report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", err.Error(), false)
			return
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", "Wait for the declared legal release to complete in qBittorrent, then retry.", false)
			return
		}
		if !waitForMoviePoll(ctx, deadline) {
			report.add("VERIFY_SERIES_EPISODE_ACQUISITION_FAILED", "legal series episode acquisition", ctx.Err().Error(), false)
			return
		}
	}
	report.add("VERIFY_SERIES_EPISODE_ACQUIRED", fmt.Sprintf("legal series episode acquisition (%s S%02dE%02d)", fixture.Spec.Title, fixture.Spec.SeasonNumber, fixture.Spec.EpisodeNumber), "", true)

	for {
		episodeFiles, observeErr := sonarrClient.ImportedEpisodeFiles(ctx, seriesID)
		if observeErr != nil {
			report.add("VERIFY_SERIES_EPISODE_IMPORT_FAILED", "Series Library import", observeErr.Error(), false)
			return
		}
		for _, episodeFile := range episodeFiles {
			importPath, pathErr := dataHostPath(environment.DataRoot, episodeFile.Path)
			if pathErr != nil {
				continue
			}
			importInfo, statErr := os.Stat(importPath)
			if statErr != nil {
				continue
			}
			for _, torrentFile := range torrentFiles {
				if torrentFile.Progress < 1 || torrentFile.Size != episodeFile.Size {
					continue
				}
				sourcePath, pathErr := dataHostPath(environment.DataRoot, filepath.ToSlash(filepath.Join(torrent.SavePath, torrentFile.Name)))
				if pathErr != nil {
					continue
				}
				sourceInfo, statErr := os.Stat(sourcePath)
				if statErr == nil && os.SameFile(sourceInfo, importInfo) {
					report.add("VERIFY_SERIES_EPISODE_HARDLINKED", "qBittorrent source and Series Library inode identity", "", true)
					verifyJellyfinSeriesEpisodePlayback(ctx, declared, request, episodeFile.Path, deadline, report)
					return
				}
			}
		}
		if time.Now().After(deadline) {
			report.add("VERIFY_SERIES_EPISODE_IMPORT_FAILED", "Series Library hardlink import", "Wait for Sonarr Completed Download Handling and confirm source/imported files share the selected Environment data root.", false)
			return
		}
		if !waitForMoviePoll(ctx, deadline) {
			report.add("VERIFY_SERIES_EPISODE_IMPORT_FAILED", "Series Library hardlink import", ctx.Err().Error(), false)
			return
		}
	}
}

func verifyJellyfinSeriesEpisodePlayback(ctx context.Context, declared config.MediaStack, request VerifyRequest, episodePath string, deadline time.Time, report *VerifyReport) {
	environment := declared.Spec.Environments[request.plan.environment]
	secretsPath := environment.SecretsFile
	if !filepath.IsAbs(secretsPath) {
		secretsPath = filepath.Join(filepath.Dir(request.plan.configPath), secretsPath)
	}
	secrets, err := decryptEnvironmentSecrets(ctx, secretsPath)
	if err != nil {
		report.add("VERIFY_SERIES_EPISODE_AUTH_FAILED", "authenticated Jellyfin Series Library access", err.Error(), false)
		return
	}
	address := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Jellyfin)
	client := jellyfin.New("http://"+address, &http.Client{Timeout: 10 * time.Second})
	for {
		ready, observeErr := client.EpisodePlaybackReady(ctx, secrets.Jellyfin, episodePath)
		if observeErr != nil {
			report.add("VERIFY_SERIES_EPISODE_PLAYBACK_FAILED", "authenticated Jellyfin series episode playback", observeErr.Error(), false)
			return
		}
		if ready {
			report.add("VERIFY_SERIES_EPISODE_PLAYBACK_READY", "authenticated Jellyfin Series Library discovery and direct playback", "", true)
			return
		}
		if time.Now().After(deadline) {
			report.add("VERIFY_SERIES_EPISODE_PLAYBACK_FAILED", "authenticated Jellyfin series episode playback", "Wait for Jellyfin to scan the Series Library and confirm direct playback is available, then retry.", false)
			return
		}
		if !waitForMoviePoll(ctx, deadline) {
			report.add("VERIFY_SERIES_EPISODE_PLAYBACK_FAILED", "authenticated Jellyfin series episode playback", ctx.Err().Error(), false)
			return
		}
	}
}
