package engine

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
)

type jellyfinPlaybackCheck struct {
	diagnosticPrefix string
	libraryName      string
	mediaName        string
	observe          func(*jellyfin.Client, context.Context, jellyfin.Credentials, string) (bool, error)
}

var (
	moviePlaybackCheck = jellyfinPlaybackCheck{
		diagnosticPrefix: "VERIFY_MOVIE",
		libraryName:      "Movie Library",
		mediaName:        "movie",
		observe:          (*jellyfin.Client).MoviePlaybackReady,
	}
	seriesEpisodePlaybackCheck = jellyfinPlaybackCheck{
		diagnosticPrefix: "VERIFY_SERIES_EPISODE",
		libraryName:      "Series Library",
		mediaName:        "series episode",
		observe:          (*jellyfin.Client).EpisodePlaybackReady,
	}
)

func verifyJellyfinPlayback(ctx context.Context, declared config.MediaStack, request VerifyRequest, mediaPath string, deadline time.Time, report *VerifyReport, check jellyfinPlaybackCheck) {
	environment := declared.Spec.Environments[request.plan.environment]
	secretsPath := environment.SecretsFile
	if !filepath.IsAbs(secretsPath) {
		secretsPath = filepath.Join(filepath.Dir(request.plan.configPath), secretsPath)
	}
	secrets, err := decryptEnvironmentSecrets(ctx, secretsPath)
	if err != nil {
		report.add(check.diagnosticPrefix+"_AUTH_FAILED", "authenticated Jellyfin "+check.libraryName+" access", err.Error(), false)
		return
	}
	address := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Jellyfin)
	client := jellyfin.New("http://"+address, &http.Client{Timeout: 10 * time.Second})
	for {
		ready, observeErr := check.observe(client, ctx, secrets.Jellyfin, mediaPath)
		if observeErr != nil {
			report.add(check.diagnosticPrefix+"_PLAYBACK_FAILED", "authenticated Jellyfin "+check.mediaName+" playback", observeErr.Error(), false)
			return
		}
		if ready {
			report.add(check.diagnosticPrefix+"_PLAYBACK_READY", "authenticated Jellyfin "+check.libraryName+" discovery and direct playback", "", true)
			return
		}
		if time.Now().After(deadline) {
			report.add(check.diagnosticPrefix+"_PLAYBACK_FAILED", "authenticated Jellyfin "+check.mediaName+" playback", "Wait for Jellyfin to scan the "+check.libraryName+" and confirm direct playback is available, then retry.", false)
			return
		}
		if !waitForMediaPoll(ctx, deadline) {
			report.add(check.diagnosticPrefix+"_PLAYBACK_FAILED", "authenticated Jellyfin "+check.mediaName+" playback", ctx.Err().Error(), false)
			return
		}
	}
}
