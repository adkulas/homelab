package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

type InitRequest struct {
	environment string
	configPath  string
	answersPath string
	answers     *initAnswers
}

type InitReport struct {
	Environment string
	Preserved   bool
}

type initAnswers struct {
	RuntimeUID              int    `yaml:"runtimeUID"`
	RuntimeGID              int    `yaml:"runtimeGID"`
	Timezone                string `yaml:"timezone"`
	Country                 string `yaml:"country"`
	ServerCategory          string `yaml:"serverCategory"`
	OpenVPNProtocol         string `yaml:"openvpnProtocol"`
	CatalogueUpdateInterval string `yaml:"catalogueUpdateInterval"`
	AgeRecipient            string `yaml:"ageRecipient"`
	ServiceUsername         string `yaml:"serviceUsername"`
	ServicePassword         string `yaml:"servicePassword"`
	ProfilarrAPIKey         string `yaml:"profilarrAPIKey"`
	JellyfinUsername        string `yaml:"jellyfinUsername"`
	JellyfinPassword        string `yaml:"jellyfinPassword"`
}

func NewInitRequest(workingDirectory, environment, configPath, answersPath string) (InitRequest, error) {
	if environment != "production" && environment != "staging" {
		return InitRequest{}, fmt.Errorf("environment %q is not production or staging", environment)
	}
	repositoryRoot, err := findRepositoryRoot(workingDirectory)
	if err != nil {
		return InitRequest{}, err
	}
	if configPath == "" {
		configPath = filepath.Join(repositoryRoot, filepath.FromSlash(defaultConfigPath))
	} else if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(workingDirectory, configPath)
	}
	if answersPath == "" {
		return InitRequest{}, fmt.Errorf("answers are required in non-interactive mode")
	}
	if !filepath.IsAbs(answersPath) {
		answersPath = filepath.Join(workingDirectory, answersPath)
	}
	return InitRequest{environment: environment, configPath: configPath, answersPath: answersPath}, nil
}

func (localEngine) Init(ctx context.Context, request InitRequest) (InitReport, error) {
	if err := ctx.Err(); err != nil {
		return InitReport{}, err
	}
	declared, err := config.Load(request.configPath)
	if err != nil {
		return InitReport{}, err
	}
	if err := declared.ValidateEnvironment(request.environment); err != nil {
		return InitReport{}, err
	}
	configurationAlreadyComplete := initializationComplete(declared)

	environment := declared.Spec.Environments[request.environment]
	secretsPath := environment.SecretsFile
	if !filepath.IsAbs(secretsPath) {
		secretsPath = filepath.Join(filepath.Dir(request.configPath), secretsPath)
	}
	secretExists := false
	if _, err := os.Stat(secretsPath); err == nil {
		secretExists = true
		if configurationAlreadyComplete {
			if err := provisionDataLayout(environment.DataRoot, declared.Spec.Defaults.RuntimeUID, declared.Spec.Defaults.RuntimeGID); err != nil {
				return InitReport{}, err
			}
			return InitReport{Environment: request.environment, Preserved: true}, nil
		}
	} else if !os.IsNotExist(err) {
		return InitReport{}, fmt.Errorf("inspect secret document: %w", err)
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
	if err := validateInitAnswers(answers, !secretExists); err != nil {
		return InitReport{}, err
	}
	if !secretExists {
		if err := encryptSecrets(ctx, secretsPath, answers); err != nil {
			return InitReport{}, err
		}
	}
	if configurationAlreadyComplete {
		if err := provisionDataLayout(environment.DataRoot, declared.Spec.Defaults.RuntimeUID, declared.Spec.Defaults.RuntimeGID); err != nil {
			return InitReport{}, err
		}
		return InitReport{Environment: request.environment}, nil
	}

	declared.Spec.Defaults.RuntimeUID = answers.RuntimeUID
	declared.Spec.Defaults.RuntimeGID = answers.RuntimeGID
	declared.Spec.Defaults.Timezone = answers.Timezone
	declared.Spec.Acquisition.VPN = config.VPN{
		Provider:                "nordvpn",
		Protocol:                "openvpn",
		OpenVPNProtocol:         answers.OpenVPNProtocol,
		Server:                  config.Server{Countries: []string{answers.Country}},
		CatalogueUpdateInterval: answers.CatalogueUpdateInterval,
	}
	if answers.ServerCategory != "" {
		declared.Spec.Acquisition.VPN.Server.Categories = []string{answers.ServerCategory}
	}
	if err := config.Write(request.configPath, declared); err != nil {
		return InitReport{}, err
	}
	if err := provisionDataLayout(environment.DataRoot, answers.RuntimeUID, answers.RuntimeGID); err != nil {
		return InitReport{}, err
	}
	return InitReport{Environment: request.environment}, nil
}

func loadInitAnswers(path string) (initAnswers, error) {
	file, err := os.Open(path)
	if err != nil {
		return initAnswers{}, fmt.Errorf("load initialization answers: %w", err)
	}
	defer file.Close()
	var answers initAnswers
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&answers); err != nil {
		return initAnswers{}, fmt.Errorf("load initialization answers: %w", err)
	}
	return answers, nil
}

func validateInitAnswers(answers initAnswers, requireSecrets bool) error {
	if answers.RuntimeUID <= 0 || answers.RuntimeGID <= 0 {
		return fmt.Errorf("runtimeUID and runtimeGID must be positive numeric identities")
	}
	if answers.Timezone == "" || answers.Country == "" {
		return fmt.Errorf("timezone and country are required")
	}
	if _, err := time.LoadLocation(answers.Timezone); err != nil {
		return fmt.Errorf("timezone %q is not a valid IANA timezone: %w", answers.Timezone, err)
	}
	if requireSecrets && (answers.AgeRecipient == "" || answers.ServiceUsername == "" || answers.ServicePassword == "" || len(answers.ProfilarrAPIKey) < 32 || answers.JellyfinUsername == "" || answers.JellyfinPassword == "") {
		return fmt.Errorf("ageRecipient, serviceUsername, servicePassword, a profilarrAPIKey of at least 32 characters, jellyfinUsername, and jellyfinPassword are required")
	}
	if answers.ServerCategory != "" && answers.ServerCategory != "P2P" {
		return fmt.Errorf("serverCategory must be empty or P2P")
	}
	if answers.OpenVPNProtocol != "udp" && answers.OpenVPNProtocol != "tcp" {
		return fmt.Errorf("openvpnProtocol must be udp or tcp")
	}
	interval, err := time.ParseDuration(answers.CatalogueUpdateInterval)
	if err != nil || interval < 360*time.Hour {
		return fmt.Errorf("catalogueUpdateInterval must be a duration of at least 360h")
	}
	return nil
}

func encryptSecrets(ctx context.Context, destination string, answers initAnswers) error {
	var document environmentSecretDocument
	document.NordVPN.OpenVPN.ServiceUsername = answers.ServiceUsername
	document.NordVPN.OpenVPN.ServicePassword = answers.ServicePassword
	document.Profilarr.APIKey = answers.ProfilarrAPIKey
	document.Jellyfin.Username = answers.JellyfinUsername
	document.Jellyfin.Password = answers.JellyfinPassword
	plain, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode environment secrets: %w", err)
	}
	command := exec.CommandContext(ctx, "sops", "encrypt", "--age", answers.AgeRecipient, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	command.Stdin = bytes.NewReader(plain)
	encrypted, err := command.Output()
	if err != nil {
		return fmt.Errorf("encrypt environment secrets with SOPS: %w", err)
	}
	if err := writeSecretAtomic(destination, encrypted); err != nil {
		return fmt.Errorf("write environment secret document: %w", err)
	}
	return nil
}

func writeSecretAtomic(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".media-stack-secret-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
