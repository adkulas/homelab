package engine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

func NewInteractiveInitRequest(workingDirectory, environment, configPath string, input io.Reader, output io.Writer) (InitRequest, error) {
	request, err := NewInitRequest(workingDirectory, environment, configPath, ".interactive")
	if err != nil {
		return InitRequest{}, err
	}
	request.answersPath = ""
	declared, err := config.Load(request.configPath)
	if err != nil {
		return InitRequest{}, err
	}
	if err := declared.ValidateEnvironment(environment); err != nil {
		return InitRequest{}, err
	}
	secretPath := declared.Spec.Environments[environment].SecretsFile
	if !filepath.IsAbs(secretPath) {
		secretPath = filepath.Join(filepath.Dir(request.configPath), secretPath)
	}
	secretExists := false
	if _, err := os.Stat(secretPath); err == nil {
		secretExists = true
		if initializationComplete(declared) {
			request.answers = &initAnswers{}
			return request, nil
		}
	} else if !os.IsNotExist(err) {
		return InitRequest{}, fmt.Errorf("inspect secret document: %w", err)
	}

	reader := bufio.NewReader(input)
	answers := answersFromDeclaredConfiguration(declared)
	identityChanged := false
	if answers.RuntimeUID <= 0 {
		answers.RuntimeUID, err = promptNumericIdentity(reader, output, "Runtime UID for intended operator", os.Getuid())
		if err != nil {
			return InitRequest{}, err
		}
		identityChanged = true
	}
	if answers.RuntimeGID <= 0 {
		answers.RuntimeGID, err = promptNumericIdentity(reader, output, "Runtime GID for intended operator", os.Getgid())
		if err != nil {
			return InitRequest{}, err
		}
		identityChanged = true
	}
	if identityChanged {
		confirmation, err := prompt(reader, output, fmt.Sprintf("Confirm runtime identity %d:%d [y/N]: ", answers.RuntimeUID, answers.RuntimeGID))
		if err != nil {
			return InitRequest{}, err
		}
		if !strings.EqualFold(confirmation, "y") && !strings.EqualFold(confirmation, "yes") {
			return InitRequest{}, fmt.Errorf("runtime identity was not confirmed")
		}
	}
	if answers.Timezone == "" {
		answers.Timezone, err = promptRequired(reader, output, "Timezone: ")
		if err != nil {
			return InitRequest{}, err
		}
	}
	if answers.Country == "" {
		answers.Country, err = promptRequired(reader, output, "NordVPN server country: ")
		if err != nil {
			return InitRequest{}, err
		}
	}
	if len(declared.Spec.Acquisition.VPN.Server.Categories) == 0 {
		answers.ServerCategory, err = prompt(reader, output, "Server category (optional; P2P or blank): ")
		if err != nil {
			return InitRequest{}, err
		}
	}
	if answers.OpenVPNProtocol == "" {
		answers.OpenVPNProtocol, err = promptDefault(reader, output, "OpenVPN protocol (udp or tcp) [udp]: ", "udp")
		if err != nil {
			return InitRequest{}, err
		}
	}
	if answers.CatalogueUpdateInterval == "" {
		answers.CatalogueUpdateInterval, err = promptDefault(reader, output, "Gluetun catalogue update interval [480h]: ", "480h")
		if err != nil {
			return InitRequest{}, err
		}
	}
	if !secretExists {
		fmt.Fprintln(output, "Use the username and password from the Nord Account manual-setup area.")
		fmt.Fprintln(output, "These are service credentials, not your Nord account email/password.")
		fmt.Fprintln(output, "No access token or API key is requested or stored.")
		answers.AgeRecipient, err = promptRequired(reader, output, "Age recipient for this environment: ")
		if err != nil {
			return InitRequest{}, err
		}
		answers.ServiceUsername, err = promptRequired(reader, output, "NordVPN OpenVPN service username: ")
		if err != nil {
			return InitRequest{}, err
		}
		answers.ServicePassword, err = promptRequired(reader, output, "NordVPN OpenVPN service password: ")
		if err != nil {
			return InitRequest{}, err
		}
	}
	request.answers = &answers
	return request, nil
}

func answersFromDeclaredConfiguration(declared config.MediaStack) initAnswers {
	vpn := declared.Spec.Acquisition.VPN
	answers := initAnswers{
		RuntimeUID:              declared.Spec.Defaults.RuntimeUID,
		RuntimeGID:              declared.Spec.Defaults.RuntimeGID,
		Timezone:                declared.Spec.Defaults.Timezone,
		OpenVPNProtocol:         vpn.OpenVPNProtocol,
		CatalogueUpdateInterval: vpn.CatalogueUpdateInterval,
	}
	if len(vpn.Server.Countries) == 1 {
		answers.Country = vpn.Server.Countries[0]
	}
	if len(vpn.Server.Categories) == 1 {
		answers.ServerCategory = vpn.Server.Categories[0]
	}
	return answers
}

func promptNumericIdentity(reader *bufio.Reader, output io.Writer, label string, suggested int) (int, error) {
	answer, err := promptDefault(reader, output, fmt.Sprintf("%s [%d]: ", label, suggested), strconv.Itoa(suggested))
	if err != nil {
		return 0, err
	}
	identity, err := strconv.Atoi(answer)
	if err != nil || identity <= 0 {
		return 0, fmt.Errorf("%s must be a positive number", label)
	}
	return identity, nil
}

func promptRequired(reader *bufio.Reader, output io.Writer, label string) (string, error) {
	answer, err := prompt(reader, output, label)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return "", fmt.Errorf("%s is required", strings.TrimSpace(strings.TrimSuffix(label, ":")))
	}
	return answer, nil
}

func promptDefault(reader *bufio.Reader, output io.Writer, label, fallback string) (string, error) {
	answer, err := prompt(reader, output, label)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

func prompt(reader *bufio.Reader, output io.Writer, label string) (string, error) {
	if _, err := fmt.Fprint(output, label); err != nil {
		return "", err
	}
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}
