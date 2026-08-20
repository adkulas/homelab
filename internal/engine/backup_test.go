package engine

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestBackupManifestCoversEveryMutableServiceVolume(t *testing.T) {
	request, err := NewBackupRequest(repositoryRoot(t), "staging", "", "before-upgrade", true)
	if err != nil {
		t.Fatalf("new backup request: %v", err)
	}
	report, err := New().Backup(context.Background(), request)
	if err != nil {
		t.Fatalf("backup report: %v", err)
	}

	if report.Environment != "staging" {
		t.Fatalf("environment = %q, want staging", report.Environment)
	}
	if report.ProjectName != "media-staging" {
		t.Fatalf("project name = %q, want media-staging", report.ProjectName)
	}
	if !report.Protected {
		t.Fatal("protected backup was not marked protected")
	}
	if report.Label != "before-upgrade" {
		t.Fatalf("label = %q, want before-upgrade", report.Label)
	}
	if len(report.ConfigDigest) != 64 {
		t.Fatalf("config digest length = %d, want 64", len(report.ConfigDigest))
	}

	got := make([]string, 0, len(report.Services))
	for _, service := range report.Services {
		got = append(got, service.Name+":"+service.Volume)
	}
	sort.Strings(got)
	want := []string{
		"jellyfin:jellyfin-config",
		"profilarr:profilarr-config",
		"prowlarr:prowlarr-config",
		"qbittorrent:qbittorrent-config",
		"radarr:radarr-config",
		"seerr:seerr-config",
		"sonarr:sonarr-config",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backup services = %#v, want %#v", got, want)
	}
}

func TestBackupManifestKeepsEnvironmentIdentityInPaths(t *testing.T) {
	for _, environment := range []string{"production", "staging"} {
		request, err := NewBackupRequest(repositoryRoot(t), environment, "", "", false)
		if err != nil {
			t.Fatalf("new backup request for %s: %v", environment, err)
		}
		report, err := New().Backup(context.Background(), request)
		if err != nil {
			t.Fatalf("backup report for %s: %v", environment, err)
		}
		if report.Environment != environment {
			t.Fatalf("environment = %q, want %q", report.Environment, environment)
		}
		if report.ProjectName != map[string]string{"production": "media-production", "staging": "media-staging"}[environment] {
			t.Fatalf("project name = %q", report.ProjectName)
		}
		found := false
		for _, service := range report.Services {
			for _, path := range service.Paths {
				if strings.Contains(path, "/srv/media/"+environment) {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("service paths do not preserve %s identity: %#v", environment, report.Services)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join("..", ".."))
}
