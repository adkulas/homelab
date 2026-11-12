package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/companions"
	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/profilarr"
	"github.com/adkulas/homelab/internal/prowlarr"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/seerr"
	"github.com/adkulas/homelab/internal/sonarr"
	"gopkg.in/yaml.v3"
)

type ApplyRequest struct {
	plan PlanRequest
}

type ApplyReport struct {
	Environment string
}

type environmentSecrets struct {
	Scope           *config.SecretScope
	Houndarr        companions.AdminCredentials
	CleanuparrKey   string
	CleanuparrAdmin companions.AdminCredentials
	OpenVPN         openVPNCredentials
	ProfilarrKey    string
	Jellyfin        jellyfin.Credentials
	QBittorrent     qbittorrent.Credentials
}

func NewApplyRequest(workingDirectory, environment, configPath string) (ApplyRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return ApplyRequest{}, err
	}
	return ApplyRequest{plan: plan}, nil
}

func (engine localEngine) Apply(ctx context.Context, request ApplyRequest) (ApplyReport, error) {
	release, err := lockEnvironment(ctx, request.plan, false)
	if err != nil {
		return ApplyReport{}, err
	}
	defer release()

	plan, err := engine.Plan(ctx, request.plan)
	if err != nil {
		return ApplyReport{}, err
	}
	declared, err := request.plan.load()
	if err != nil {
		return ApplyReport{}, err
	}
	versions, err := config.LoadVersions(request.plan.versionsPath)
	if err != nil {
		return ApplyReport{}, err
	}
	policyPath := filepath.Join(filepath.Dir(request.plan.versionsPath), "fixtures", "profilarr-movie-policy.yaml")
	moviePolicy, err := radarr.LoadMoviePolicy(policyPath, versions.Policy.ProfilarrPCDRevision)
	if err != nil {
		return ApplyReport{}, err
	}
	seriesPolicyPath := filepath.Join(filepath.Dir(request.plan.versionsPath), "fixtures", "profilarr-series-policy.yaml")
	seriesPolicy, err := sonarr.LoadSeriesPolicy(seriesPolicyPath, versions.Policy.ProfilarrPCDRevision)
	if err != nil {
		return ApplyReport{}, err
	}
	environment := declared.Spec.Environments[request.plan.environment]
	secrets, restoreDrillConfirmed, err := restoreDrillSecretsForApply(ctx, environment.BackupRoot, environment.ProjectName, environment)
	if err != nil {
		return ApplyReport{}, err
	}
	if !restoreDrillConfirmed {
		secrets, err = decryptSelectedEnvironmentSecrets(ctx, request.plan.configPath, environment)
		if err != nil {
			return ApplyReport{}, err
		}
	}
	credentials := secrets.OpenVPN
	if err := materializeRuntimeSecrets(environmentRuntimeSecretDirectory(environment), secrets); err != nil {
		return ApplyReport{}, fmt.Errorf("materialize runtime secrets: %w", err)
	}

	if output, err := runDockerCompose(ctx, plan, "up", "-d", "gluetun"); err != nil {
		return ApplyReport{}, fmt.Errorf("start healthy Gluetun: %w: %s", err, redactCredentials(output, credentials))
	}
	if err := waitForHealthyGluetun(ctx, plan, 120*time.Second); err != nil {
		return ApplyReport{}, err
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "qbittorrent"); err != nil {
		return ApplyReport{}, fmt.Errorf("start qBittorrent after healthy Gluetun: %w: %s", err, redactCredentials(output, credentials))
	}
	qbittorrentStart, err := qbittorrentContainerStart(ctx, plan)
	if err != nil {
		return ApplyReport{}, err
	}
	address := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.QBittorrent)
	client := qbittorrent.New("http://"+address, &http.Client{Timeout: 10 * time.Second})
	qbittorrentConfiguration := qbittorrent.DeclaredConfiguration{Credentials: secrets.QBittorrent, Port: environment.Ports.QBittorrent}
	if err := client.Bootstrap(ctx, secrets.QBittorrent, func(ctx context.Context) (string, bool, error) {
		return currentTemporaryQBittorrentPassword(ctx, plan, qbittorrentStart)
	}, 120*time.Second, 2*time.Second); err != nil {
		return ApplyReport{}, fmt.Errorf("establish declared qBittorrent authentication: %w", err)
	}
	if _, err := client.ReconcileAcquisitionPolicy(ctx); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile qBittorrent acquisition policy: %w", err)
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "radarr"); err != nil {
		return ApplyReport{}, fmt.Errorf("start Radarr: %w: %s", err, redactCredentials(output, credentials))
	}
	apiKey, err := waitForRadarrAPIKey(ctx, plan, 120*time.Second)
	if err != nil {
		return ApplyReport{}, err
	}
	radarrAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Radarr)
	radarrClient := radarr.New("http://"+radarrAddress, apiKey, &http.Client{Timeout: 10 * time.Second})
	if err := waitForRadarrReady(ctx, radarrClient, 120*time.Second); err != nil {
		return ApplyReport{}, err
	}
	if _, err := radarrClient.ReconcileMovieLibrary(ctx, qbittorrentConfiguration); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Radarr Movie Library: %w", err)
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "sonarr"); err != nil {
		return ApplyReport{}, fmt.Errorf("start Sonarr: %w: %s", err, redactCredentials(output, credentials))
	}
	sonarrAPIKey, err := waitForServiceAPIKey(ctx, plan, "sonarr", "Sonarr", 120*time.Second)
	if err != nil {
		return ApplyReport{}, err
	}
	sonarrAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Sonarr)
	sonarrClient := sonarr.New("http://"+sonarrAddress, sonarrAPIKey, &http.Client{Timeout: 10 * time.Second})
	if err := waitForSonarrReady(ctx, sonarrClient, 120*time.Second); err != nil {
		return ApplyReport{}, err
	}
	if _, err := sonarrClient.ReconcileSeriesLibrary(ctx, qbittorrentConfiguration); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Sonarr Series Library: %w", err)
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "prowlarr"); err != nil {
		return ApplyReport{}, fmt.Errorf("start Prowlarr: %w: %s", err, redactCredentials(output, credentials))
	}
	prowlarrAPIKey, err := waitForServiceAPIKey(ctx, plan, "prowlarr", "Prowlarr", 120*time.Second)
	if err != nil {
		return ApplyReport{}, err
	}
	prowlarrAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Prowlarr)
	prowlarrClient := prowlarr.New("http://"+prowlarrAddress, prowlarrAPIKey, &http.Client{Timeout: 10 * time.Second})
	if err := waitForProwlarrReady(ctx, prowlarrClient, 120*time.Second); err != nil {
		return ApplyReport{}, err
	}
	if _, err := prowlarrClient.ReconcileLibraryDiscovery(ctx, apiKey, sonarrAPIKey); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Prowlarr library discovery: %w", err)
	}
	profilarrAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Profilarr)
	verifyDelays := func(ctx context.Context) error {
		if err := profilarr.VerifyZeroDelay(ctx, "http://"+radarrAddress, apiKey); err != nil {
			return err
		}
		return profilarr.VerifyZeroDelay(ctx, "http://"+sonarrAddress, sonarrAPIKey)
	}
	bootstrapDirectory := filepath.Join(environment.BackupRoot, "profilarr-bootstrap", environment.ProjectName)
	if err := profilarr.CheckPending(bootstrapDirectory); err != nil {
		return ApplyReport{}, err
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "profilarr"); err != nil {
		return ApplyReport{}, fmt.Errorf("start Profilarr: %w: %s", err, redactCredentials(output, credentials))
	}
	if err := ensureProfilarrPolicy(ctx, plan, profilarr.Request{
		Image: versions.Images["profilarr"], Revision: versions.Policy.ProfilarrPCDRevision,
		BackupDirectory: bootstrapDirectory, RadarrKey: apiKey, SonarrKey: sonarrAPIKey,
	}, "http://"+profilarrAddress, secrets.ProfilarrKey, func(ctx context.Context) error {
		if err := radarrClient.VerifyMoviePolicy(ctx, moviePolicy); err != nil {
			return err
		}
		if err := sonarrClient.VerifySeriesPolicy(ctx, seriesPolicy); err != nil {
			return err
		}
		return verifyDelays(ctx)
	}); err != nil {
		return ApplyReport{}, err
	}
	if err := radarrClient.VerifyMoviePolicy(ctx, moviePolicy); err != nil {
		return ApplyReport{}, fmt.Errorf("manual action required: open %s, link %s at pinned revision %s, select the %q quality profile, %q naming preset, Movie quality definitions, and %q media-management preset for Radarr, run sync, then rerun media-stack apply: %w",
			"http://"+profilarrAddress,
			moviePolicy.Source.Repository,
			moviePolicy.Source.Revision,
			moviePolicy.Profile.Name,
			moviePolicy.Naming.Preset,
			moviePolicy.MediaManagement.Preset,
			err,
		)
	}
	if err := sonarrClient.VerifySeriesPolicy(ctx, seriesPolicy); err != nil {
		return ApplyReport{}, fmt.Errorf("manual action required: open %s, link %s at pinned revision %s, select the %q quality profile, %q naming preset, Series quality definitions, and %q media-management preset for Sonarr, run sync, then rerun media-stack apply: %w",
			"http://"+profilarrAddress,
			seriesPolicy.Source.Repository,
			seriesPolicy.Source.Revision,
			seriesPolicy.Profile.Name,
			seriesPolicy.Naming.Preset,
			seriesPolicy.MediaManagement.Preset,
			err,
		)
	}
	if err := verifyDelays(ctx); err != nil {
		return ApplyReport{}, fmt.Errorf("manual action required: open %s, synchronize the Default zero-delay profile for both Arr services, then rerun media-stack apply: %w", "http://"+profilarrAddress, err)
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "jellyfin"); err != nil {
		return ApplyReport{}, fmt.Errorf("start Jellyfin: %w: %s", err, redactCredentials(output, credentials))
	}
	jellyfinAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Jellyfin)
	jellyfinClient := jellyfin.New("http://"+jellyfinAddress, &http.Client{Timeout: 10 * time.Second})
	if err := waitForJellyfinLibraries(ctx, jellyfinClient, secrets.Jellyfin, 120*time.Second); err != nil {
		return ApplyReport{}, err
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "seerr"); err != nil {
		return ApplyReport{}, fmt.Errorf("start Seerr: %w: %s", err, redactCredentials(output, credentials))
	}
	seerrAddress := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Seerr)
	seerrClient, err := seerr.New("http://"+seerrAddress, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return ApplyReport{}, err
	}
	seerrCredentials := seerr.Credentials{Username: secrets.Jellyfin.Username, Password: secrets.Jellyfin.Password}
	if err := waitForSeerrAuthentication(ctx, seerrClient, seerrCredentials, 120*time.Second); err != nil {
		return ApplyReport{}, err
	}
	if err := seerrClient.ReconcileRadarr(ctx, apiKey, moviePolicy.Profile.Name); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Seerr Movie Library requests: %w", err)
	}
	if err := seerrClient.ReconcileSonarr(ctx, sonarrAPIKey, seriesPolicy.Profile.Name, seriesAutomationEnabled(environment)); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Seerr Series Library requests: %w", err)
	}
	if environment.SeriesAutomation != nil {
		if output, err := runDockerCompose(ctx, plan, "up", "-d", "houndarr", "cleanuparr"); err != nil {
			return ApplyReport{}, fmt.Errorf("start series companions: %w: %s", err, redactCredentials(output, credentials))
		}
		if !restoreDrillConfirmed {
			if err := bootstrapSeriesCompanions(ctx, request.plan.configPath, environment, &secrets, sonarrAPIKey); err != nil {
				return ApplyReport{}, err
			}
		}
		if err := reconcileSeriesCompanions(ctx, environment, secrets, sonarrAPIKey, false); err != nil {
			return ApplyReport{}, err
		}
	}
	if restoreDrillConfirmed {
		if err := completeRestoreDrillGate(environment.BackupRoot); err != nil {
			return ApplyReport{}, fmt.Errorf("complete Restore Drill integration gate: %w", err)
		}
	}
	return ApplyReport{Environment: request.plan.environment}, nil
}

func waitForSeerrAuthentication(ctx context.Context, client *seerr.Client, credentials seerr.Credentials, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var lastErr error
	for {
		if err := client.ReconcileAuthentication(ctx, credentials); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Seerr API did not become ready within %s: %w", timeout, lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}
func waitForJellyfinLibraries(ctx context.Context, client *jellyfin.Client, credentials jellyfin.Credentials, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		err := client.ReconcileLibraries(ctx, credentials)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Jellyfin API did not become ready within %s: %w", timeout, err)
		case <-time.After(2 * time.Second):
		}
	}
}

func observeProfilarrBootstrap(ctx context.Context, baseURL, apiKey string) (bool, bool, error) {
	connections, retry, err := profilarr.Observe(ctx, baseURL, apiKey)
	return profilarr.Complete(connections), retry, err
}

func waitForRadarrAPIKey(ctx context.Context, plan Plan, timeout time.Duration) (string, error) {
	return waitForServiceAPIKey(ctx, plan, "radarr", "Radarr", timeout)
}

func waitForServiceAPIKey(ctx context.Context, plan Plan, service, application string, timeout time.Duration) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		output, err := runDockerCompose(ctx, plan, "exec", "-T", service, "cat", "/config/config.xml")
		if err == nil {
			var document struct {
				APIKey string `xml:"ApiKey"`
			}
			if xml.Unmarshal(output, &document) == nil && document.APIKey != "" {
				return document.APIKey, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("%s did not publish its API key within %s", application, timeout)
		case <-time.After(2 * time.Second):
		}
	}
}

type radarrReadiness interface {
	Ready(context.Context) error
}

type prowlarrReadiness interface {
	Ready(context.Context) error
}

type sonarrReadiness interface {
	Ready(context.Context) error
}

func waitForSonarrReady(ctx context.Context, client sonarrReadiness, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var lastErr error
	for {
		if err := client.Ready(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Sonarr API did not become ready within %s: %w", timeout, lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}

func waitForProwlarrReady(ctx context.Context, client prowlarrReadiness, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var lastErr error
	for {
		if err := client.Ready(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Prowlarr API did not become ready within %s: %w", timeout, lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}

func waitForRadarrReady(ctx context.Context, client radarrReadiness, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var lastErr error
	for {
		if err := client.Ready(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Radarr API did not become ready within %s: %w", timeout, lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}

var temporaryPasswordPattern = regexp.MustCompile(`(?i)temporary password[^\r\n:]*session:\s*(?:\x1b\[[0-9;]*m\s*)*([^\s\x1b]+)`)

func temporaryQBittorrentPassword(logs []byte) (string, error) {
	matches := temporaryPasswordPattern.FindAllSubmatch(logs, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("qBittorrent did not report its temporary Web UI password; restore the CLI-owned bootstrap state or provide a supported credential contract")
	}
	return string(matches[len(matches)-1][1]), nil
}

func currentTemporaryQBittorrentPassword(ctx context.Context, plan Plan, startedAt time.Time) (string, bool, error) {
	logs, err := runDockerCompose(ctx, plan, "logs", "--no-color", "--since", startedAt.Format(time.RFC3339Nano), "qbittorrent")
	if err != nil {
		return "", false, fmt.Errorf("read current-start qBittorrent bootstrap credentials: %w", err)
	}
	password, err := temporaryQBittorrentPassword(logs)
	if err != nil {
		if strings.Contains(strings.ToLower(string(logs)), "password was not set") {
			return "", false, qbittorrent.ErrCurrentStartCredentialMissing
		}
		return "", false, nil
	}
	return password, true, nil
}

func qbittorrentContainerStart(ctx context.Context, plan Plan) (time.Time, error) {
	containerID, err := runDockerCompose(ctx, plan, "ps", "-q", "qbittorrent")
	if err != nil {
		return time.Time{}, fmt.Errorf("resolve current qBittorrent container identity: %w", err)
	}
	if len(bytes.TrimSpace(containerID)) == 0 {
		return time.Time{}, fmt.Errorf("resolve current qBittorrent container identity: Compose returned no container ID")
	}
	command := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.StartedAt}}", string(bytes.TrimSpace(containerID)))
	output, err := command.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("inspect current qBittorrent container start: %w", err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, string(bytes.TrimSpace(output)))
	if err != nil {
		return time.Time{}, fmt.Errorf("decode current qBittorrent container start: %w", err)
	}
	return startedAt, nil
}

func waitForTemporaryQBittorrentPassword(ctx context.Context, plan Plan, timeout time.Duration) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		logs, err := runDockerCompose(ctx, plan, "logs", "--no-color", "qbittorrent")
		if err != nil {
			return "", fmt.Errorf("read qBittorrent bootstrap credentials: %w", err)
		}
		if password, err := temporaryQBittorrentPassword(logs); err == nil {
			return password, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("qBittorrent did not report its temporary Web UI password within %s", timeout)
		case <-time.After(2 * time.Second):
		}
	}
}

func environmentAddress(bindAddress string, port int) string {
	if bindAddress == "0.0.0.0" || bindAddress == "::" || bindAddress == "" {
		bindAddress = "127.0.0.1"
	}
	return net.JoinHostPort(bindAddress, fmt.Sprint(port))
}

func runDockerCompose(ctx context.Context, plan Plan, arguments ...string) ([]byte, error) {
	commandArguments := append([]string{"compose", "-f", "-"}, arguments...)
	command := exec.CommandContext(ctx, "docker", commandArguments...)
	command.Stdin = bytes.NewReader(plan.Compose())
	return command.CombinedOutput()
}

func waitForHealthyGluetun(ctx context.Context, plan Plan, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		output, err := runDockerCompose(ctx, plan, "ps", "--format", "json", "gluetun")
		if err != nil {
			return fmt.Errorf("observe Gluetun health: %w: %s", err, bytes.TrimSpace(output))
		}
		var status struct {
			Health string `json:"Health"`
			State  string `json:"State"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(output), &status); err != nil {
			return fmt.Errorf("decode Gluetun health: %w", err)
		}
		if status.State == "running" && status.Health == "healthy" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Gluetun did not become healthy within %s (state %q, health %q)", timeout, status.State, status.Health)
		case <-time.After(2 * time.Second):
		}
	}
}

func redactCredentials(output []byte, credentials openVPNCredentials) string {
	return strings.NewReplacer(
		credentials.Username, "[REDACTED]",
		credentials.Password, "[REDACTED]",
	).Replace(string(bytes.TrimSpace(output)))
}

type openVPNCredentials struct {
	Username string
	Password string
}

func readSecretDocument(ctx context.Context, path string, destination any) error {
	command := exec.CommandContext(ctx, "sops", "decrypt", "--output-type", "yaml", path)
	plain, err := command.Output()
	if err != nil {
		return fmt.Errorf("decrypt selected credential document with SOPS")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(plain))
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid selected credential document schema")
	}
	return nil
}

func decryptEnvironmentSecrets(ctx context.Context, path string) (environmentSecrets, error) {
	var document environmentSecretDocument
	if err := readSecretDocument(ctx, path, &document); err != nil {
		return environmentSecrets{}, err
	}
	return environmentSecretsFromDocument(document)
}

func environmentSecretsFromDocument(document environmentSecretDocument) (environmentSecrets, error) {
	credentials := openVPNCredentials{
		Username: document.NordVPN.OpenVPN.ServiceUsername,
		Password: document.NordVPN.OpenVPN.ServicePassword,
	}
	if document.Scope == nil && (credentials.Username == "" || credentials.Password == "") {
		return environmentSecrets{}, fmt.Errorf("selected Environment secrets require NordVPN OpenVPN serviceUsername and servicePassword")
	}
	if len(document.Profilarr.APIKey) < 32 {
		return environmentSecrets{}, fmt.Errorf("selected Environment secrets require profilarr.apiKey with at least 32 characters")
	}
	if document.Jellyfin.Username == "" || document.Jellyfin.Password == "" {
		return environmentSecrets{}, fmt.Errorf("selected Environment secrets require jellyfin.username and jellyfin.password")
	}
	if document.QBittorrent.Username == "" || document.QBittorrent.Password == "" {
		return environmentSecrets{}, fmt.Errorf("selected Environment secrets require qbittorrent.username and qbittorrent.password; run media-stack init with the new keys to migrate this SOPS document without overwriting existing values")
	}
	return environmentSecrets{Scope: document.Scope, Houndarr: companions.AdminCredentials{Username: document.Houndarr.Username, Password: document.Houndarr.Password}, CleanuparrKey: document.Cleanuparr.APIKey, CleanuparrAdmin: companions.AdminCredentials{Username: document.Cleanuparr.Username, Password: document.Cleanuparr.Password}, OpenVPN: credentials, ProfilarrKey: document.Profilarr.APIKey, Jellyfin: jellyfin.Credentials{Username: document.Jellyfin.Username, Password: document.Jellyfin.Password}, QBittorrent: qbittorrent.Credentials{Username: document.QBittorrent.Username, Password: document.QBittorrent.Password}}, nil
}

func decryptSelectedEnvironmentSecrets(ctx context.Context, configPath string, environment config.Environment) (environmentSecrets, error) {
	path := selectedSecretPath(configPath, environment)
	var document environmentSecretDocument
	if err := readSecretDocument(ctx, path, &document); err != nil {
		return environmentSecrets{}, err
	}
	if environment.SecretScope != nil {
		if document.Scope == nil || *document.Scope != *environment.SecretScope {
			return environmentSecrets{}, fmt.Errorf("application secret ownership does not match selected Site, Stack Instance and Environment")
		}
		if document.NordVPN.OpenVPN.ServiceUsername != "" || document.NordVPN.OpenVPN.ServicePassword != "" {
			return environmentSecrets{}, fmt.Errorf("owned application secrets must use the separate Site VPN document")
		}
	} else if document.Scope != nil {
		return environmentSecrets{}, fmt.Errorf("owned application credentials require explicit Site and Stack Instance selection")
	}
	secrets, err := environmentSecretsFromDocument(document)
	if err != nil {
		return environmentSecrets{}, err
	}
	if environment.SecretScope != nil {
		credentials, err := decryptSiteVPN(ctx, environment.VPNSecretsFile, environment.SecretScope.Site)
		if err != nil {
			return environmentSecrets{}, err
		}
		secrets.OpenVPN = credentials
	}
	return secrets, nil
}

func decryptSiteVPN(ctx context.Context, path, site string) (openVPNCredentials, error) {
	var document siteVPNSecretDocument
	if err := readSecretDocument(ctx, path, &document); err != nil {
		return openVPNCredentials{}, err
	}
	if document.Site != site {
		return openVPNCredentials{}, fmt.Errorf("VPN secret ownership does not match selected Site")
	}
	credentials := openVPNCredentials{Username: document.NordVPN.OpenVPN.ServiceUsername, Password: document.NordVPN.OpenVPN.ServicePassword}
	if credentials.Username == "" || credentials.Password == "" {
		return openVPNCredentials{}, fmt.Errorf("Site VPN secrets require OpenVPN serviceUsername and servicePassword")
	}
	return credentials, nil
}

func decryptRestoreCredentials(ctx context.Context, path string, environment config.Environment) (environmentSecrets, error) {
	if environment.SecretScope == nil {
		return decryptEnvironmentSecrets(ctx, path)
	}
	environment.SecretsFile = path
	return decryptSelectedEnvironmentSecrets(ctx, "", environment)
}

func materializeRuntimeSecrets(directory string, secrets environmentSecrets) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"openvpn_user":     secrets.OpenVPN.Username,
		"openvpn_password": secrets.OpenVPN.Password,
		"profilarr.env":    "PROFILARR_API_KEY=" + secrets.ProfilarrKey,
	} {
		if err := writeSecretAtomic(filepath.Join(directory, name), []byte(value+"\n")); err != nil {
			return err
		}
	}
	return nil
}
