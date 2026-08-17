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

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/prowlarr"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"github.com/adkulas/homelab/internal/radarr"
	"gopkg.in/yaml.v3"
)

type ApplyRequest struct {
	plan PlanRequest
}

type ApplyReport struct {
	Environment string
}

func NewApplyRequest(workingDirectory, environment, configPath string) (ApplyRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return ApplyRequest{}, err
	}
	return ApplyRequest{plan: plan}, nil
}

func (engine localEngine) Apply(ctx context.Context, request ApplyRequest) (ApplyReport, error) {
	plan, err := engine.Plan(ctx, request.plan)
	if err != nil {
		return ApplyReport{}, err
	}
	declared, err := config.Load(request.plan.configPath)
	if err != nil {
		return ApplyReport{}, err
	}
	environment := declared.Spec.Environments[request.plan.environment]
	secretPath := environment.SecretsFile
	if !filepath.IsAbs(secretPath) {
		secretPath = filepath.Join(filepath.Dir(request.plan.configPath), secretPath)
	}
	credentials, err := decryptOpenVPNCredentials(ctx, secretPath)
	if err != nil {
		return ApplyReport{}, err
	}
	if err := materializeOpenVPNCredentials(runtimeSecretDirectory(environment.ProjectName), credentials); err != nil {
		return ApplyReport{}, fmt.Errorf("materialize Gluetun runtime secrets: %w", err)
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
	password, err := waitForTemporaryQBittorrentPassword(ctx, plan, 120*time.Second)
	if err != nil {
		return ApplyReport{}, err
	}
	address := environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.QBittorrent)
	client := qbittorrent.New("http://"+address, &http.Client{Timeout: 10 * time.Second})
	if err := client.Login(ctx, "admin", password); err != nil {
		return ApplyReport{}, err
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
	if _, err := radarrClient.ReconcileMovieLibrary(ctx, password); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Radarr Movie Library: %w", err)
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
	if _, err := prowlarrClient.ReconcileMovieDiscovery(ctx, apiKey); err != nil {
		return ApplyReport{}, fmt.Errorf("reconcile Prowlarr movie discovery: %w", err)
	}
	return ApplyReport{Environment: request.plan.environment}, nil
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

var temporaryPasswordPattern = regexp.MustCompile(`temporary password is provided for this session:\s*(\S+)`)

func temporaryQBittorrentPassword(logs []byte) (string, error) {
	matches := temporaryPasswordPattern.FindAllSubmatch(logs, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("qBittorrent did not report its temporary Web UI password; restore the CLI-owned bootstrap state or provide a supported credential contract")
	}
	return string(matches[len(matches)-1][1]), nil
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

func decryptOpenVPNCredentials(ctx context.Context, path string) (openVPNCredentials, error) {
	command := exec.CommandContext(ctx, "sops", "decrypt", "--output-type", "yaml", path)
	plain, err := command.Output()
	if err != nil {
		return openVPNCredentials{}, fmt.Errorf("decrypt selected Environment secrets: %w", err)
	}
	var document struct {
		NordVPN struct {
			OpenVPN struct {
				ServiceUsername string `yaml:"serviceUsername"`
				ServicePassword string `yaml:"servicePassword"`
			} `yaml:"openvpn"`
		} `yaml:"nordvpn"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(plain))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return openVPNCredentials{}, fmt.Errorf("decode selected Environment secrets: %w", err)
	}
	credentials := openVPNCredentials{
		Username: document.NordVPN.OpenVPN.ServiceUsername,
		Password: document.NordVPN.OpenVPN.ServicePassword,
	}
	if credentials.Username == "" || credentials.Password == "" {
		return openVPNCredentials{}, fmt.Errorf("selected Environment secrets require NordVPN OpenVPN serviceUsername and servicePassword")
	}
	return credentials, nil
}

func materializeOpenVPNCredentials(directory string, credentials openVPNCredentials) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"openvpn_user":     credentials.Username,
		"openvpn_password": credentials.Password,
	} {
		if err := writeSecretAtomic(filepath.Join(directory, name), []byte(value+"\n")); err != nil {
			return err
		}
	}
	return nil
}
