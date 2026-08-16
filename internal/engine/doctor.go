package engine

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

const doctorSchemaVersion = "homelab.media-stack/doctor/v1alpha1"

type DoctorRequest struct{ environment, configPath, versionsPath string }

type Diagnostic struct {
	Code        string `json:"code"`
	Status      string `json:"status"`
	Severity    string `json:"severity"`
	Environment string `json:"environment"`
	Subject     string `json:"subject"`
	Explanation string `json:"explanation"`
	Remedy      string `json:"remedy"`
	Retryable   bool   `json:"retryable"`
}

type DoctorReport struct {
	SchemaVersion string       `json:"schemaVersion"`
	Environment   string       `json:"environment"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

func NewDoctorRequest(workingDirectory, environment, configPath string) (DoctorRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return DoctorRequest{}, err
	}
	return DoctorRequest{environment: environment, configPath: plan.configPath, versionsPath: plan.versionsPath}, nil
}

func (report DoctorReport) Failed() bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Status == "fail" {
			return true
		}
	}
	return false
}

func (localEngine) Doctor(ctx context.Context, request DoctorRequest) (DoctorReport, error) {
	declared, err := config.Load(request.configPath)
	if err != nil {
		return DoctorReport{}, err
	}
	if err := declared.ValidateEnvironment(request.environment); err != nil {
		return DoctorReport{}, err
	}
	versions, err := config.LoadVersions(request.versionsPath)
	if err != nil {
		return DoctorReport{}, err
	}
	environment := declared.Spec.Environments[request.environment]
	secretPath := environment.SecretsFile
	if !filepath.IsAbs(secretPath) {
		secretPath = filepath.Join(filepath.Dir(request.configPath), secretPath)
	}
	vpn := declared.Spec.Acquisition.VPN
	report := DoctorReport{SchemaVersion: doctorSchemaVersion, Environment: request.environment}
	add := func(code, subject, remedy string, passed bool) {
		status, severity, explanation := "pass", "info", subject+" is available"
		if !passed {
			status, severity, explanation = "fail", "error", subject+" is unavailable or unsupported"
		}
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: code, Status: status, Severity: severity,
			Environment: request.environment, Subject: subject, Explanation: explanation, Remedy: remedy, Retryable: !passed})
	}
	add("PREFLIGHT_PLATFORM_UNSUPPORTED", "supported Ubuntu or WSL2 host", "Run inside Ubuntu or a WSL2 distribution integrated with Docker Desktop.", supportedPlatform())
	add("DEPENDENCY_DOCKER_UNAVAILABLE", "Docker Engine", "Install Docker Engine or enable Docker Desktop WSL integration.", runQuiet(ctx, "docker", "version"))
	add("DEPENDENCY_COMPOSE_UNAVAILABLE", "Docker Compose", "Install or enable the Docker Compose v2 plugin.", runQuiet(ctx, "docker", "compose", "version"))
	add("DEPENDENCY_SOPS_UNAVAILABLE", "SOPS", "Install SOPS and ensure it is on PATH.", runQuiet(ctx, "sops", "--version"))
	add("DEPENDENCY_AGE_UNAVAILABLE", "age", "Install age and ensure it is on PATH.", runQuiet(ctx, "age", "--version"))
	add("SECRET_DECRYPT_FAILED", "selected Environment secret decryption", "Install the matching age identity and verify the SOPS document can be decrypted.", runQuiet(ctx, "sops", "decrypt", "--output-type", "yaml", secretPath))
	image := versions.Images["gluetun"]
	add("PREFLIGHT_TUN_UNAVAILABLE", "/dev/net/tun in the pinned Gluetun image", "Enable the TUN device for Docker and rerun doctor.", runQuiet(ctx, "docker", "run", "--rm", "--device", "/dev/net/tun", "--entrypoint", "/bin/sh", image, "-c", "test -c /dev/net/tun"))
	add("PREFLIGHT_NET_ADMIN_UNAVAILABLE", "NET_ADMIN for the pinned Gluetun image", "Allow Docker to grant NET_ADMIN to the Gluetun container.", runQuiet(ctx, "docker", "run", "--rm", "--cap-add", "NET_ADMIN", "--entrypoint", "/bin/sh", image, "-c", "ip link add ms-doctor type dummy && ip link delete ms-doctor"))
	filterArguments := []string{"run", "--rm", "-e", "VPN_SERVICE_PROVIDER=nordvpn", "-e", "VPN_TYPE=openvpn", "-e", "OPENVPN_PROTOCOL=" + vpn.OpenVPNProtocol}
	if len(vpn.Server.Countries) > 0 {
		filterArguments = append(filterArguments, "-e", "SERVER_COUNTRIES="+strings.Join(vpn.Server.Countries, ","))
	}
	if len(vpn.Server.Categories) > 0 {
		filterArguments = append(filterArguments, "-e", "SERVER_CATEGORIES="+strings.Join(vpn.Server.Categories, ","))
	}
	filterArguments = append(filterArguments, image, "format-servers", "-nordvpn")
	validFilter := vpn.Provider == "nordvpn" && vpn.Protocol == "openvpn" && (vpn.OpenVPNProtocol == "udp" || vpn.OpenVPNProtocol == "tcp") && len(vpn.Server.Countries) > 0
	add("PREFLIGHT_VPN_FILTER_UNSUPPORTED", "declared NordVPN OpenVPN server filters", "Choose protocol, country, and category values supported by the pinned Gluetun catalogue.", validFilter && runNonEmpty(ctx, "docker", filterArguments...))
	return report, nil
}

func runQuiet(ctx context.Context, name string, arguments ...string) bool {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	return command.Run() == nil
}

func runNonEmpty(ctx context.Context, name string, arguments ...string) bool {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Stderr = io.Discard
	output, err := command.Output()
	return err == nil && len(strings.TrimSpace(string(output))) > 0
}

func supportedPlatform() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if contents, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil && strings.Contains(strings.ToLower(string(contents)), "microsoft") {
		lower := strings.ToLower(string(contents))
		return strings.Contains(lower, "wsl2") || strings.Contains(lower, "microsoft-standard")
	}
	contents, err := os.ReadFile("/etc/os-release")
	return err == nil && strings.Contains(strings.ToLower(string(contents)), "id=ubuntu")
}
