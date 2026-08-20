package acceptance_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBackupRendersCompleteServiceCoverageForSelectedEnvironment(t *testing.T) {
	command := backupCommand(t, "--environment", "staging", "--output", "json", "--label", "before-upgrade", "--protect")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack backup failed: %v\n%s", err, output)
	}
	var report struct {
		Environment string `json:"environment"`
		ProjectName string `json:"projectName"`
		Protected   bool   `json:"protected"`
		Label       string `json:"label"`
		Services    []struct {
			Name   string   `json:"name"`
			Volume string   `json:"volume"`
			Paths  []string `json:"paths"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode backup report: %v\n%s", err, output)
	}
	if report.Environment != "staging" || report.ProjectName != "media-staging" {
		t.Fatalf("unexpected environment identity: %#v", report)
	}
	if !report.Protected || report.Label != "before-upgrade" {
		t.Fatalf("unexpected backup flags: %#v", report)
	}
	if len(report.Services) != 7 {
		t.Fatalf("services = %d, want 7", len(report.Services))
	}
	want := map[string]string{
		"qbittorrent": "qbittorrent-config",
		"prowlarr":    "prowlarr-config",
		"sonarr":      "sonarr-config",
		"radarr":      "radarr-config",
		"profilarr":   "profilarr-config",
		"jellyfin":    "jellyfin-config",
		"seerr":       "seerr-config",
	}
	for _, service := range report.Services {
		if want[service.Name] != service.Volume {
			t.Fatalf("service %q volume = %q, want %q", service.Name, service.Volume, want[service.Name])
		}
	}
}

func backupCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	goArguments := append([]string{"run", "../../cmd/media-stack", "backup"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
	return command
}
