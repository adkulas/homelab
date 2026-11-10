package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/adkulas/homelab/internal/companions"
	"github.com/adkulas/homelab/internal/config"
)

// NewCompanionInitRequest adds missing companion credentials to an existing
// Environment, preserving its configuration and all existing secret values.
func NewCompanionInitRequest(workingDirectory, environment, configPath, answersPath string, deployments ...*Deployment) (InitRequest, error) {
	request, err := NewInitRequest(workingDirectory, environment, configPath, answersPath, deployments...)
	request.companionsOnly = true
	return request, err
}

func NewInteractiveCompanionInitRequest(ctx context.Context, workingDirectory, environment, configPath string, input io.Reader, output io.Writer, deployments ...*Deployment) (InitRequest, error) {
	request, err := NewCompanionInitRequest(workingDirectory, environment, configPath, ".interactive", deployments...)
	if err != nil {
		return InitRequest{}, err
	}
	declared, err := request.load()
	if err != nil {
		return InitRequest{}, err
	}
	selected, ok := declared.Spec.Environments[environment]
	if !ok || selected.SeriesAutomation == nil {
		return InitRequest{}, fmt.Errorf("declare seriesAutomation for the selected Environment before initializing companions")
	}
	secrets, err := decryptSelectedEnvironmentSecrets(ctx, request.configPath, selected)
	if err != nil {
		return InitRequest{}, err
	}
	answers := initAnswers{HoundarrUsername: secrets.Houndarr.Username, HoundarrPassword: secrets.Houndarr.Password, CleanuparrUsername: secrets.CleanuparrAdmin.Username, CleanuparrPassword: secrets.CleanuparrAdmin.Password}
	fmt.Fprintln(output, "Choose companion administrator credentials. Existing credentials are preserved; passwords are hidden and saved with SOPS.")
	if err := promptCompanionAnswers(bufio.NewReader(input), input, output, &answers, secrets.CleanuparrKey != ""); err != nil {
		return InitRequest{}, err
	}
	request.answers = &answers
	request.answersPath = ""
	return request, nil
}

func initializeCompanionSecrets(ctx context.Context, request InitRequest, environment config.Environment) (InitReport, error) {
	if environment.SeriesAutomation == nil {
		return InitReport{}, fmt.Errorf("declare seriesAutomation for the selected Environment before initializing companions")
	}
	path := selectedSecretPath(request.configPath, environment)
	secrets, err := decryptSelectedEnvironmentSecrets(ctx, request.configPath, environment)
	if err != nil {
		return InitReport{}, err
	}
	houndMissing := secrets.Houndarr.Username == "" || secrets.Houndarr.Password == ""
	cleanupMissing := secrets.CleanuparrKey == "" && (secrets.CleanuparrAdmin.Username == "" || secrets.CleanuparrAdmin.Password == "")
	if !houndMissing && !cleanupMissing {
		return InitReport{Environment: request.environment, Preserved: true}, nil
	}
	var answers initAnswers
	if request.answers != nil {
		answers = *request.answers
	} else {
		answers, err = loadInitAnswers(request.answersPath)
		if err != nil {
			return InitReport{}, err
		}
	}
	branches := map[string]any{}
	for _, account := range []struct {
		name, branch      string
		missing           bool
		current, provided companions.AdminCredentials
	}{
		{"Houndarr", "houndarr", houndMissing, secrets.Houndarr, companions.AdminCredentials{Username: answers.HoundarrUsername, Password: answers.HoundarrPassword}},
		{"Cleanuparr", "cleanuparr", cleanupMissing, secrets.CleanuparrAdmin, companions.AdminCredentials{Username: answers.CleanuparrUsername, Password: answers.CleanuparrPassword}},
	} {
		if !account.missing {
			continue
		}
		desired := account.current
		if desired.Username == "" {
			desired.Username = account.provided.Username
		}
		if desired.Password == "" {
			desired.Password = account.provided.Password
		}
		if err := companions.ValidateAdminCredentials(account.name, desired); err != nil {
			return InitReport{}, err
		}
		branches[account.branch] = map[string]string{"username": desired.Username, "password": desired.Password}
	}
	if err := patchEncryptedSecrets(ctx, path, branches, environment); err != nil {
		return InitReport{}, err
	}
	return InitReport{Environment: request.environment}, nil
}
