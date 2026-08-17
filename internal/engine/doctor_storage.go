package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type storageProbeCheck struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Explanation string `json:"explanation,omitempty"`
}

type storageProbeReport struct {
	Checks []storageProbeCheck `json:"checks"`
}

type storageProbeInvocation struct {
	service     string
	image       string
	hostMount   string
	targetMount string
	source      string
	destination string
}

var storageCheckDiagnostics = map[string]struct {
	code    string
	subject string
	remedy  string
}{
	"runtime_identity": {
		code: "PREFLIGHT_STORAGE_RUNTIME_IDENTITY", subject: "declared application runtime identity",
		remedy: "Set runtimeUID and runtimeGID to an identity supported by the selected data root.",
	},
	"permissions": {
		code: "PREFLIGHT_STORAGE_PERMISSIONS", subject: "application write permissions",
		remedy: "Grant the declared runtime identity create, write, rename, link, and remove access to the selected data root.",
	},
	"same_device": {
		code: "PREFLIGHT_STORAGE_SAME_DEVICE", subject: "same-device download and library paths",
		remedy: "Place torrents and media below one filesystem-backed Environment data root.",
	},
	"hardlink": {
		code: "PREFLIGHT_STORAGE_HARDLINK", subject: "hardlink creation",
		remedy: "Use storage and mount options that permit hardlinks between torrent and library paths.",
	},
	"inode_identity": {
		code: "PREFLIGHT_STORAGE_INODE_IDENTITY", subject: "hardlink inode preservation",
		remedy: "Use a filesystem that preserves inode identity for hardlinked files.",
	},
	"atomic_rename": {
		code: "PREFLIGHT_STORAGE_ATOMIC_RENAME", subject: "atomic rename",
		remedy: "Use a filesystem and mount that support atomic rename within the data root.",
	},
	"filesystem_events": {
		code: "PREFLIGHT_STORAGE_FILESYSTEM_EVENTS", subject: "filesystem events",
		remedy: "Use a filesystem and Docker mount that deliver create and rename events to application containers.",
	},
	"cleanup": {
		code: "PREFLIGHT_STORAGE_CLEANUP", subject: "storage probe cleanup",
		remedy: "Remove the reported probe directory and grant the declared runtime identity removal permission.",
	},
}

func storageDiagnostics(ctx context.Context, environment, dataRoot string, uid, gid int, images map[string]string) []Diagnostic {
	probes := []storageProbeInvocation{
		{
			service: "qBittorrent", image: images["qbittorrent"],
			hostMount: filepath.Join(dataRoot, "torrents"), targetMount: "/data/torrents",
			source: "/data/torrents", destination: "/data/torrents",
		},
		{
			service: "Radarr", image: images["radarr"],
			hostMount: dataRoot, targetMount: "/data",
			source: "/data/torrents/movies", destination: "/data/media/movies",
		},
		{
			service: "Sonarr", image: images["sonarr"],
			hostMount: dataRoot, targetMount: "/data",
			source: "/data/torrents/series", destination: "/data/media/series",
		},
	}
	var diagnostics []Diagnostic
	for _, probe := range probes {
		report, err := runStorageProbe(ctx, probe, uid, gid)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "PREFLIGHT_STORAGE_PROBE_UNAVAILABLE", Status: "fail", Severity: "error",
				Environment: environment, Subject: probe.service + " container-visible storage",
				Explanation: "the disposable storage probe could not run: " + err.Error(),
				Remedy:      "Verify Docker can run the pinned image with the declared identity and selected bind mount.", Retryable: true,
			})
			continue
		}
		if err := validateStorageProbeReport(report); err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "PREFLIGHT_STORAGE_PROBE_INVALID", Status: "fail", Severity: "error",
				Environment: environment, Subject: probe.service + " container-visible storage",
				Explanation: "the disposable storage probe returned an invalid report: " + err.Error(),
				Remedy:      "Use the media-stack binary and pinned application images from the same checked-in version.",
				Retryable:   false,
			})
			continue
		}
		for _, check := range report.Checks {
			definition, exists := storageCheckDiagnostics[check.Name]
			if !exists {
				continue
			}
			status, severity := "pass", "info"
			explanation := probe.service + " verified " + definition.subject
			if !check.Passed {
				status, severity = "fail", "error"
				explanation = probe.service + " could not verify " + definition.subject
				if check.Explanation != "" {
					explanation += ": " + check.Explanation
				}
			}
			diagnostics = append(diagnostics, Diagnostic{
				Code: definition.code, Status: status, Severity: severity, Environment: environment,
				Subject: probe.service + " " + definition.subject, Explanation: explanation,
				Remedy: definition.remedy, Retryable: !check.Passed,
			})
		}
	}
	return diagnostics
}

func validateStorageProbeReport(report storageProbeReport) error {
	seen := make(map[string]bool, len(report.Checks))
	for _, check := range report.Checks {
		if _, expected := storageCheckDiagnostics[check.Name]; !expected {
			return fmt.Errorf("unknown check %q", check.Name)
		}
		if seen[check.Name] {
			return fmt.Errorf("duplicate check %q", check.Name)
		}
		seen[check.Name] = true
	}
	for name := range storageCheckDiagnostics {
		if !seen[name] {
			return fmt.Errorf("missing check %q", name)
		}
	}
	return nil
}

func runStorageProbe(ctx context.Context, probe storageProbeInvocation, uid, gid int) (storageProbeReport, error) {
	executable, err := os.Executable()
	if err != nil {
		return storageProbeReport{}, fmt.Errorf("locate probe executable: %w", err)
	}
	arguments := []string{
		"run", "--rm",
		"--user", strconv.Itoa(uid) + ":" + strconv.Itoa(gid),
		"--mount", "type=bind,source=" + probe.hostMount + ",target=" + probe.targetMount,
		"--mount", "type=bind,source=" + executable + ",target=/media-stack-doctor-probe,readonly",
		"--entrypoint", "/media-stack-doctor-probe",
		probe.image,
		"__storage-probe", "--source", probe.source, "--destination", probe.destination,
		"--uid", strconv.Itoa(uid), "--gid", strconv.Itoa(gid),
	}
	command := exec.CommandContext(ctx, "docker", arguments...)
	output, err := command.Output()
	if err != nil {
		return storageProbeReport{}, err
	}
	var report storageProbeReport
	if err := json.Unmarshal(output, &report); err != nil {
		return storageProbeReport{}, fmt.Errorf("decode probe result: %w", err)
	}
	return report, nil
}
