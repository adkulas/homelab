package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

const (
	verifySchemaVersion = "homelab.media-stack/verify/v1alpha1"
	publicIPEchoURL     = "https://api.ipify.org"
)

type VerifyRequest struct {
	plan                   PlanRequest
	suite                  string
	legalFixturePath       string
	legalSeriesFixturePath string
}

type VerifyReport struct {
	SchemaVersion  string       `json:"schemaVersion"`
	Environment    string       `json:"environment"`
	Suite          string       `json:"suite"`
	ConfigDigest   string       `json:"configDigest"`
	VersionsDigest string       `json:"versionsDigest"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
}

func NewVerifyRequest(workingDirectory, environment, configPath, suite, legalFixturePath, legalSeriesFixturePath string) (VerifyRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return VerifyRequest{}, err
	}
	if legalFixturePath != "" && !filepath.IsAbs(legalFixturePath) {
		legalFixturePath = filepath.Join(workingDirectory, legalFixturePath)
	}
	if legalSeriesFixturePath != "" && !filepath.IsAbs(legalSeriesFixturePath) {
		legalSeriesFixturePath = filepath.Join(workingDirectory, legalSeriesFixturePath)
	}
	return VerifyRequest{plan: plan, suite: suite, legalFixturePath: legalFixturePath, legalSeriesFixturePath: legalSeriesFixturePath}, nil
}

func (report VerifyReport) Failed() bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Status == "fail" {
			return true
		}
	}
	return false
}

func (engine localEngine) Verify(ctx context.Context, request VerifyRequest) (report VerifyReport, returnError error) {
	declared, err := config.Load(request.plan.configPath)
	if err != nil {
		return report, err
	}
	versions, err := config.LoadVersions(request.plan.versionsPath)
	if err != nil {
		return report, err
	}
	report = VerifyReport{SchemaVersion: verifySchemaVersion, Environment: request.plan.environment, Suite: request.suite, ConfigDigest: fileDigest(request.plan.configPath), VersionsDigest: fileDigest(request.plan.versionsPath)}
	if report.ConfigDigest == "" || report.VersionsDigest == "" {
		return report, fmt.Errorf("calculate verification digests")
	}
	if err := declared.ValidateEnvironment(request.plan.environment); err != nil {
		return report, err
	}
	if len(declared.Spec.Acquisition.VPN.Server.Countries) == 0 {
		report.add("VERIFY_SERVER_SELECTION_EMPTY", "VPN server selection", "Declare at least one NordVPN server country.", false)
		return report, nil
	}
	if !runQuiet(ctx, "docker", "run", "--rm", "--device", "/dev/net/tun", "--entrypoint", "/bin/sh", versions.Images["gluetun"], "-c", "test -c /dev/net/tun") {
		report.add("VERIFY_TUN_UNAVAILABLE", "Gluetun TUN device", "Enable /dev/net/tun for the selected Environment and rerun apply.", false)
		return report, nil
	}
	report.add("VERIFY_TUN_AVAILABLE", "Gluetun TUN device", "", true)

	plan, err := engine.Plan(ctx, request.plan)
	if err != nil {
		return report, err
	}
	healthy, err := gluetunHealthy(ctx, plan)
	if err != nil {
		return report, err
	}
	if !healthy {
		logs, _ := runDockerCompose(ctx, plan, "logs", "--no-color", "gluetun")
		if credentialFailure(logs) {
			report.add("VERIFY_INVALID_SERVICE_CREDENTIALS", "NordVPN OpenVPN service credentials", "Replace the selected Environment's NordVPN manual-setup service credentials and rerun apply.", false)
		} else {
			report.add("VERIFY_TUNNEL_UNHEALTHY", "healthy Gluetun tunnel", "Inspect Gluetun health and logs, correct the tunnel failure, and rerun apply.", false)
		}
		return report, nil
	}
	report.add("VERIFY_TUNNEL_HEALTHY", "healthy Gluetun tunnel", "", true)

	hostIP, err := hostPublicIP(ctx)
	if err != nil {
		report.add("VERIFY_HOST_EGRESS_UNAVAILABLE", "host public-IP observation", "Restore host DNS and outbound HTTPS access, then retry.", false)
		return report, nil
	}
	tunnelIP, err := namespacePublicIP(ctx, plan)
	if err != nil {
		report.add("VERIFY_TUNNEL_UNHEALTHY", "qBittorrent namespace DNS and outbound connectivity", "Restore Gluetun health and qBittorrent connectivity, then retry.", false)
		return report, nil
	}
	if hostIP.Equal(tunnelIP) {
		report.add("VERIFY_EGRESS_LEAKED", "qBittorrent VPN egress", "Stop acquisition and correct the shared-namespace or tunnel configuration before retrying.", false)
		return report, nil
	}
	report.add("VERIFY_VPN_EGRESS", fmt.Sprintf("qBittorrent VPN egress (%s differs from host %s)", tunnelIP, hostIP), "", true)

	interrupted := false
	defer func() {
		if !interrupted {
			return
		}
		if err := restoreTunnel(context.WithoutCancel(ctx), plan); err != nil && returnError == nil {
			report.add("VERIFY_RECOVERY_FAILED", "Gluetun and qBittorrent recovery", "Start Gluetun, wait for health, restart qBittorrent, and rerun verification.", false)
		}
	}()
	if _, err := runDockerCompose(ctx, plan, "stop", "gluetun"); err != nil {
		report.add("VERIFY_TUNNEL_UNHEALTHY", "controlled Gluetun interruption", "Ensure the selected Environment is running and retry.", false)
		return report, nil
	}
	interrupted = true
	if leaked, err := namespacePublicIP(ctx, plan); err == nil {
		report.add("VERIFY_EGRESS_LEAKED", fmt.Sprintf("fail-closed qBittorrent egress (observed %s while Gluetun was stopped)", leaked), "Stop acquisition and correct Gluetun firewall enforcement before retrying.", false)
		return report, nil
	}
	report.add("VERIFY_FAIL_CLOSED", "fresh qBittorrent egress while Gluetun is stopped", "", true)

	if err := restoreTunnel(ctx, plan); err != nil {
		report.add("VERIFY_RECOVERY_FAILED", "Gluetun and qBittorrent recovery", "Start Gluetun, wait for health, restart qBittorrent, and rerun verification.", false)
		return report, nil
	}
	interrupted = false
	recoveredIP, err := namespacePublicIP(ctx, plan)
	if err != nil {
		report.add("VERIFY_RECOVERY_FAILED", "tunneled qBittorrent egress recovery", "Restore Gluetun health and qBittorrent's shared namespace, then retry.", false)
		return report, nil
	}
	if recoveredIP.Equal(hostIP) {
		report.add("VERIFY_EGRESS_LEAKED", fmt.Sprintf("recovered qBittorrent egress (observed host address %s)", recoveredIP), "Stop acquisition and correct the shared-namespace or tunnel configuration before retrying.", false)
		return report, nil
	}
	report.add("VERIFY_TUNNEL_RECOVERED", "tunneled qBittorrent egress recovery", "", true)
	if request.suite == "promotion" {
		if request.legalFixturePath != "" {
			verifyLegalMovie(ctx, plan, declared, request, &report)
		}
		if !report.Failed() && request.legalSeriesFixturePath != "" {
			verifyLegalSeries(ctx, plan, declared, request, &report)
		}
	}
	return report, nil
}

func (report *VerifyReport) add(code, subject, remedy string, passed bool) {
	status, severity, explanation := "pass", "info", subject+" passed"
	if !passed {
		status, severity, explanation = "fail", "error", subject+" failed"
	}
	report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: code, Status: status, Severity: severity,
		Environment: report.Environment, Subject: subject, Explanation: explanation, Remedy: remedy, Retryable: !passed})
}

func gluetunHealthy(ctx context.Context, plan Plan) (bool, error) {
	output, err := runDockerCompose(ctx, plan, "ps", "--format", "json", "gluetun")
	if err != nil {
		return false, fmt.Errorf("observe Gluetun health: %w: %s", err, bytes.TrimSpace(output))
	}
	var status struct {
		Health string `json:"Health"`
		State  string `json:"State"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &status); err != nil {
		return false, fmt.Errorf("decode Gluetun health: %w", err)
	}
	return status.State == "running" && status.Health == "healthy", nil
}

func credentialFailure(logs []byte) bool {
	lower := strings.ToLower(string(logs))
	return strings.Contains(lower, "auth_failed") || strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "credentials are incorrect")
}

func hostPublicIP(ctx context.Context) (net.IP, error) {
	command := exec.CommandContext(ctx, "curl", "--ipv4", "--fail", "--silent", "--show-error", "--max-time", "10", publicIPEchoURL)
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return parsePublicIP(output)
}

func namespacePublicIP(ctx context.Context, plan Plan) (net.IP, error) {
	output, err := runDockerCompose(ctx, plan, "exec", "-T", "qbittorrent", "curl", "--ipv4", "--fail", "--silent", "--show-error", "--max-time", "10", publicIPEchoURL)
	if err != nil {
		return nil, err
	}
	return parsePublicIP(output)
}

func fileDigest(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return checksum(contents)
}

func parsePublicIP(output []byte) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(string(output)))
	if ip == nil {
		return nil, fmt.Errorf("public-IP service returned an invalid address")
	}
	return ip, nil
}

func restoreTunnel(ctx context.Context, plan Plan) error {
	if output, err := runDockerCompose(ctx, plan, "start", "gluetun"); err != nil {
		return fmt.Errorf("start Gluetun: %w: %s", err, bytes.TrimSpace(output))
	}
	if err := waitForHealthyGluetun(ctx, plan, 120*time.Second); err != nil {
		return err
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "--force-recreate", "qbittorrent"); err != nil {
		return fmt.Errorf("restore qBittorrent shared namespace: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}
