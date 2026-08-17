package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
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
	return ApplyReport{Environment: request.plan.environment}, nil
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
