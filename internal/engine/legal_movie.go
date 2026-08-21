package engine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/radarr"
	"gopkg.in/yaml.v3"
)

const legalMovieSchemaVersion = "homelab.media-stack/legal-movie/v1alpha1"

type legalMovieFixture struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Title        string `yaml:"title"`
		TMDBID       int    `yaml:"tmdbId"`
		ReleaseTitle string `yaml:"releaseTitle"`
		Indexer      string `yaml:"indexer"`
		Timeout      string `yaml:"timeout"`
	} `yaml:"spec"`
}

func loadLegalMovieFixture(path string) (legalMovieFixture, time.Duration, error) {
	var fixture legalMovieFixture
	file, err := os.Open(path)
	if err != nil {
		return fixture, 0, fmt.Errorf("open legal movie fixture: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return fixture, 0, fmt.Errorf("decode legal movie fixture: %w", err)
	}
	if fixture.APIVersion != legalMovieSchemaVersion || fixture.Kind != "LegalMovieFixture" {
		return fixture, 0, fmt.Errorf("unsupported legal movie fixture %q %q", fixture.APIVersion, fixture.Kind)
	}
	if fixture.Spec.Title == "" || fixture.Spec.TMDBID <= 0 || fixture.Spec.ReleaseTitle == "" || fixture.Spec.Indexer != "Internet Archive" {
		return fixture, 0, fmt.Errorf("legal movie fixture requires title, positive tmdbId, releaseTitle, and approved Internet Archive indexer")
	}
	timeout, err := time.ParseDuration(fixture.Spec.Timeout)
	if err != nil || timeout <= 0 {
		return fixture, 0, fmt.Errorf("legal movie fixture timeout must be a positive duration")
	}
	return fixture, timeout, nil
}

func verifyLegalMovie(ctx context.Context, plan Plan, declared config.MediaStack, request VerifyRequest, report *VerifyReport) {
	fixture, timeout, err := loadLegalMovieFixture(request.legalFixturePath)
	if err != nil {
		report.add("VERIFY_LEGAL_FIXTURE_INVALID", "legal movie fixture", err.Error(), false)
		return
	}
	environment := declared.Spec.Environments[request.plan.environment]
	password, err := waitForTemporaryQBittorrentPassword(ctx, plan, 120*time.Second)
	if err != nil {
		report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", err.Error(), false)
		return
	}
	qbAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.QBittorrent)
	qbClient := qbittorrent.New("http://"+qbAddress, &http.Client{Timeout: 10 * time.Second})
	if err := qbClient.Login(ctx, "admin", password); err != nil {
		report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", err.Error(), false)
		return
	}
	apiKey, err := waitForRadarrAPIKey(ctx, plan, 120*time.Second)
	if err != nil {
		report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", err.Error(), false)
		return
	}
	radarrAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Radarr)
	radarrClient := radarr.New("http://"+radarrAddress, apiKey, &http.Client{Timeout: 10 * time.Second})
	movieID, err := radarrClient.AcquireLegalMovie(ctx, fixture.Spec.TMDBID, fixture.Spec.ReleaseTitle, fixture.Spec.Indexer)
	if err != nil {
		report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", err.Error(), false)
		return
	}

	deadline := time.Now().Add(timeout)
	var torrent qbittorrent.Torrent
	var torrentFiles []qbittorrent.TorrentFile
	for {
		var complete bool
		torrent, torrentFiles, complete, err = qbClient.CompletedMovie(ctx, fixture.Spec.ReleaseTitle)
		if err != nil {
			report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", err.Error(), false)
			return
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", "Wait for the declared legal release to complete in qBittorrent, then retry.", false)
			return
		}
		if !waitForMoviePoll(ctx, deadline) {
			report.add("VERIFY_MOVIE_ACQUISITION_FAILED", "legal movie acquisition", ctx.Err().Error(), false)
			return
		}
	}
	report.add("VERIFY_MOVIE_ACQUIRED", fmt.Sprintf("legal movie acquisition (%s)", fixture.Spec.Title), "", true)

	for {
		movieFiles, observeErr := radarrClient.ImportedMovieFiles(ctx, movieID)
		if observeErr != nil {
			report.add("VERIFY_MOVIE_IMPORT_FAILED", "Movie Library import", observeErr.Error(), false)
			return
		}
		for _, movieFile := range movieFiles {
			importPath, pathErr := dataHostPath(environment.DataRoot, movieFile.Path)
			if pathErr != nil {
				continue
			}
			importInfo, statErr := os.Stat(importPath)
			if statErr != nil {
				continue
			}
			for _, torrentFile := range torrentFiles {
				if torrentFile.Progress < 1 || torrentFile.Size != movieFile.Size {
					continue
				}
				sourcePath, pathErr := dataHostPath(environment.DataRoot, filepath.ToSlash(filepath.Join(torrent.SavePath, torrentFile.Name)))
				if pathErr != nil {
					continue
				}
				sourceInfo, statErr := os.Stat(sourcePath)
				if statErr == nil && os.SameFile(sourceInfo, importInfo) {
					report.add("VERIFY_MOVIE_HARDLINKED", "qBittorrent source and Movie Library inode identity", "", true)
					verifyJellyfinMoviePlayback(ctx, declared, request, movieFile.Path, deadline, report)
					return
				}
			}
		}
		if time.Now().After(deadline) {
			report.add("VERIFY_MOVIE_IMPORT_FAILED", "Movie Library hardlink import", "Wait for Radarr Completed Download Handling and confirm source/imported files share the selected Environment data root.", false)
			return
		}
		if !waitForMoviePoll(ctx, deadline) {
			report.add("VERIFY_MOVIE_IMPORT_FAILED", "Movie Library hardlink import", ctx.Err().Error(), false)
			return
		}
	}
}

func verifyJellyfinMoviePlayback(ctx context.Context, declared config.MediaStack, request VerifyRequest, moviePath string, deadline time.Time, report *VerifyReport) {
	environment := declared.Spec.Environments[request.plan.environment]
	secretsPath := environment.SecretsFile
	if !filepath.IsAbs(secretsPath) {
		secretsPath = filepath.Join(filepath.Dir(request.plan.configPath), secretsPath)
	}
	secrets, err := decryptEnvironmentSecrets(ctx, secretsPath)
	if err != nil {
		report.add("VERIFY_MOVIE_AUTH_FAILED", "authenticated Jellyfin Movie Library access", err.Error(), false)
		return
	}
	address := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Jellyfin)
	client := jellyfin.New("http://"+address, &http.Client{Timeout: 10 * time.Second})
	for {
		ready, observeErr := client.MoviePlaybackReady(ctx, secrets.Jellyfin, moviePath)
		if observeErr != nil {
			report.add("VERIFY_MOVIE_PLAYBACK_FAILED", "authenticated Jellyfin movie playback", observeErr.Error(), false)
			return
		}
		if ready {
			report.add("VERIFY_MOVIE_PLAYBACK_READY", "authenticated Jellyfin Movie Library discovery and direct playback", "", true)
			return
		}
		if time.Now().After(deadline) {
			report.add("VERIFY_MOVIE_PLAYBACK_FAILED", "authenticated Jellyfin movie playback", "Wait for Jellyfin to scan the Movie Library and confirm direct playback is available, then retry.", false)
			return
		}
		if !waitForMoviePoll(ctx, deadline) {
			report.add("VERIFY_MOVIE_PLAYBACK_FAILED", "authenticated Jellyfin movie playback", ctx.Err().Error(), false)
			return
		}
	}
}

func waitForMoviePoll(ctx context.Context, deadline time.Time) bool {
	wait := 250 * time.Millisecond
	if remaining := time.Until(deadline); remaining < wait {
		wait = remaining
	}
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func dataHostPath(root, containerPath string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(containerPath))
	if clean != "/data" && !strings.HasPrefix(clean, "/data/") {
		return "", fmt.Errorf("path %q is outside /data", containerPath)
	}
	relative := strings.TrimPrefix(clean, "/data")
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}
