package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/companions"
	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/seerr"
)

func seriesAutomationEnabled(environment config.Environment) bool {
	return environment.SeriesAutomation != nil && environment.SeriesAutomation.Enabled
}
func companionAddress(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

func bootstrapSeriesCompanions(ctx context.Context, configPath string, environment config.Environment, secrets *environmentSecrets, sonarrKey string) error {
	if err := companions.ValidateAdminCredentials("Houndarr", secrets.Houndarr); err != nil {
		return fmt.Errorf("run init --companions for the selected Environment: %w", err)
	}
	if secrets.CleanuparrKey == "" {
		if err := companions.ValidateAdminCredentials("Cleanuparr", secrets.CleanuparrAdmin); err != nil {
			return fmt.Errorf("run init --companions for the selected Environment: %w", err)
		}
		cleanup := companions.NewCleanuparr(companionAddress(environment.Ports.Cleanuparr), "", &http.Client{})
		var key string
		err := waitForCompanionBootstrap(ctx, func(ctx context.Context) error {
			var err error
			key, err = cleanup.Bootstrap(ctx, secrets.CleanuparrAdmin)
			return err
		})
		if err != nil {
			return err
		}
		branch := map[string]string{"username": secrets.CleanuparrAdmin.Username, "password": secrets.CleanuparrAdmin.Password, "apiKey": key}
		if err := patchEncryptedSecrets(ctx, selectedSecretPath(configPath, environment), map[string]any{"cleanuparr": branch}); err != nil {
			return err
		}
		secrets.CleanuparrKey = key
	}
	hound := companions.NewHoundarr(companionAddress(environment.Ports.Houndarr), &http.Client{})
	return waitForCompanionBootstrap(ctx, func(ctx context.Context) error {
		return hound.Bootstrap(ctx, secrets.Houndarr, *environment.SeriesAutomation, sonarrKey)
	})
}

func waitForCompanionBootstrap(ctx context.Context, bootstrap func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	for {
		err := bootstrap(ctx)
		var failure *companions.Failure
		if !errors.As(err, &failure) || failure.Code != "VERIFY_COMPANION_UNAVAILABLE" {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}

func reconcileSeriesCompanions(ctx context.Context, environment config.Environment, secrets environmentSecrets, sonarrKey string, observeOnly bool) error {
	if environment.SeriesAutomation == nil {
		return nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	hound := companions.NewHoundarr(companionAddress(environment.Ports.Houndarr), client)
	if err := hound.Verify(ctx, secrets.Houndarr, *environment.SeriesAutomation); err != nil {
		return err
	}
	cleanup := companions.NewCleanuparr(companionAddress(environment.Ports.Cleanuparr), secrets.CleanuparrKey, client)
	qbit := qbittorrent.DeclaredConfiguration{Credentials: secrets.QBittorrent, Port: environment.Ports.QBittorrent}
	return cleanup.Reconcile(ctx, *environment.SeriesAutomation, sonarrKey, qbit, observeOnly)
}

func verifySeriesCompanions(ctx context.Context, plan Plan, declared config.MediaStack, request VerifyRequest, report *VerifyReport) {
	environment := declared.Spec.Environments[request.plan.environment]
	if environment.SeriesAutomation == nil {
		report.add("VERIFY_COMPANIONS_UNDECLARED", "Series automation policy", "Declare Environment seriesAutomation settings before verifying companions.", false)
		return
	}
	secrets, err := decryptSelectedEnvironmentSecrets(ctx, request.plan.configPath, environment)
	if err != nil {
		report.add("VERIFY_COMPANION_CREDENTIALS_INVALID", "Companion credentials", "Correct the selected Environment SOPS document.", false)
		return
	}
	sonarrKey, err := observeServiceAPIKey(ctx, plan, "sonarr")
	if err != nil {
		report.add("VERIFY_COMPANION_TARGET_UNAVAILABLE", "Environment Sonarr", "Start Sonarr and rerun apply.", false)
		return
	}
	if err := reconcileSeriesCompanions(ctx, environment, secrets, sonarrKey, true); err != nil {
		var failure *companions.Failure
		if errors.As(err, &failure) {
			report.add(failure.Code, "Series companion configuration", failure.Remedy, false)
		} else {
			report.add("VERIFY_COMPANION_UNAVAILABLE", "Series companion configuration", "Check companion service health.", false)
		}
		return
	}
	seerrClient, err := seerr.New("http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Seerr), observationHTTPClient())
	if err != nil || seerrClient.VerifySonarrSearchOwnership(ctx, seerr.Credentials{Username: secrets.Jellyfin.Username, Password: secrets.Jellyfin.Password}, seriesAutomationEnabled(environment)) != nil {
		report.add("VERIFY_REQUEST_ROUTING_FAILED", "Seerr-to-Sonarr request ownership", "Run apply to restore Environment-local Seerr routing and disable grouped searches when Houndarr owns acquisition.", false)
		return
	}
	report.add("VERIFY_REQUEST_ROUTING_CONVERGED", "Seerr request monitoring with declared search ownership", "", true)
	report.add("VERIFY_COMPANION_POLICY_CONVERGED", "Houndarr proactive episode searches and Cleanuparr queue recovery ownership", "", true)
	observeCompanionActivity(ctx, environment, secrets, report)
	if !report.Failed() && request.recoveryFixturePath != "" {
		verifySeriesRecovery(ctx, environment, secrets, "http://"+environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Sonarr), sonarrKey, request.recoveryFixturePath, report)
	}
}

func observeCompanionActivity(ctx context.Context, environment config.Environment, secrets environmentSecrets, report *VerifyReport) {
	client := &http.Client{Timeout: 10 * time.Second}
	hound := companions.NewHoundarr(companionAddress(environment.Ports.Houndarr), client)
	if err := hound.Verify(ctx, secrets.Houndarr, *environment.SeriesAutomation); err != nil {
		addCompanionFailure(report, err)
		return
	}
	searches, err := hound.ObserveSearches(ctx)
	if err != nil {
		addCompanionFailure(report, err)
		return
	}
	reported := map[string]bool{}
	for _, search := range searches {
		code := ""
		if strings.Contains(search.Reason, "hourly cap") {
			code = "VERIFY_SEARCH_CAP_EXHAUSTED"
		}
		if strings.Contains(search.Reason, "queue backpressure") {
			code = "VERIFY_SEARCH_BACKPRESSURE"
		}
		if search.Action == "error" {
			code = "VERIFY_EPISODE_SEARCH_DISPATCH_FAILED"
		}
		if code != "" && !reported[code] {
			reported[code] = true
			report.add(code, "Observed Houndarr search scheduling", "Inspect Houndarr's authenticated logs for item details; adjust the declared budget only after review.", search.Action != "error")
		}
	}
	cleanup := companions.NewCleanuparr(companionAddress(environment.Ports.Cleanuparr), secrets.CleanuparrKey, client)
	events, err := cleanup.ObserveQueue(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		addCompanionFailure(report, err)
		return
	}
	reasons := map[string]string{"StalledStrike": "VERIFY_STALLED_DETECTED", "DownloadingMetadataStrike": "VERIFY_METADATA_STUCK_DETECTED", "SlowSpeedStrike": "VERIFY_SLOW_SPEED_DETECTED", "SlowTimeStrike": "VERIFY_SLOW_COMPLETION_DETECTED", "FailedImportStrike": "VERIFY_FAILED_IMPORT_DETECTED"}
	for _, event := range events {
		if event.IsDryRun {
			continue
		}
		if code := reasons[event.EventType]; code != "" {
			subject := fmt.Sprintf("Observed %s; strike count %d of %d", event.EventType, event.StrikeCount, environment.SeriesAutomation.Recovery.Strikes)
			report.add(code, subject, "", true)
			if event.StrikeCount >= environment.SeriesAutomation.Recovery.Strikes {
				report.add("VERIFY_CLEANUP_THRESHOLD_REACHED", "Declared qualifying-observation threshold reached", "Inspect Cleanuparr's removal and Sonarr blocklist evidence.", true)
			}
		}
		if event.EventType == "QueueItemDeleted" {
			report.add("VERIFY_QUEUE_REMOVAL_OBSERVED", "Cleanuparr queue removal event observed", "Use a recovery fixture to prove persisted blocklisting and replacement scope.", true)
		}
		if event.EventType == "SearchTriggered" && event.SearchType == "Replacement" {
			report.add("VERIFY_REPLACEMENT_DISPATCHED", "Cleanuparr replacement search event observed", "Use a recovery fixture to prove the Sonarr command's episode or season context.", event.SearchStatus != "Failed")
			if event.SearchStatus == "Failed" {
				report.add("VERIFY_REPLACEMENT_FAILED", "Cleanuparr replacement search failed", "Inspect Sonarr command and Cleanuparr logs.", false)
			}
		}
	}
}

func addCompanionFailure(report *VerifyReport, err error) {
	var failure *companions.Failure
	if errors.As(err, &failure) {
		report.add(failure.Code, "Series companion observation", failure.Remedy, false)
	} else {
		report.add("VERIFY_COMPANION_UNAVAILABLE", "Series companion observation", "Check companion service health.", false)
	}
}
