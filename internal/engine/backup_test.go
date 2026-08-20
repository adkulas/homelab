package engine

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
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

func TestApplyRetentionKeepsDailyWeeklyMonthlySurvivors(t *testing.T) {
	archives := []BackupArchive{
		{ID: "2026-08-20-a", GeneratedAt: mustTime("2026-08-20T09:00:00Z")},
		{ID: "2026-08-20-b", GeneratedAt: mustTime("2026-08-20T18:00:00Z")},
		{ID: "2026-08-19", GeneratedAt: mustTime("2026-08-19T10:00:00Z")},
		{ID: "2026-08-13", GeneratedAt: mustTime("2026-08-13T10:00:00Z")},
		{ID: "2026-07-31", GeneratedAt: mustTime("2026-07-31T10:00:00Z")},
		{ID: "2026-06-30", GeneratedAt: mustTime("2026-06-30T10:00:00Z")},
		{ID: "2026-05-15", GeneratedAt: mustTime("2026-05-15T10:00:00Z")},
	}

	decision := ApplyRetention(RetentionPolicy{Daily: 1, Weekly: 1, Monthly: 1}, archives, mustTime("2026-08-20T23:59:59Z"))

	gotKeep := archiveIDs(decision.Keep)
	wantKeep := []string{"2026-08-19", "2026-08-20-a", "2026-08-20-b"}
	if !reflect.DeepEqual(gotKeep, wantKeep) {
		t.Fatalf("kept archives = %#v, want %#v", gotKeep, wantKeep)
	}
	gotDrop := archiveIDs(decision.Drop)
	wantDrop := []string{"2026-05-15", "2026-06-30", "2026-07-31", "2026-08-13"}
	if !reflect.DeepEqual(gotDrop, wantDrop) {
		t.Fatalf("dropped archives = %#v, want %#v", gotDrop, wantDrop)
	}
}

func TestApplyRetentionNeverDropsProtectedArchives(t *testing.T) {
	archives := []BackupArchive{
		{ID: "protected-old", GeneratedAt: mustTime("2026-01-01T10:00:00Z"), Protected: true},
		{ID: "protected-new", GeneratedAt: mustTime("2026-08-20T10:00:00Z"), Protected: true},
		{ID: "expired", GeneratedAt: mustTime("2026-02-01T10:00:00Z")},
	}

	decision := ApplyRetention(RetentionPolicy{}, archives, mustTime("2026-08-20T23:59:59Z"))

	gotKeep := archiveIDs(decision.Keep)
	wantKeep := []string{"protected-new", "protected-old"}
	if !reflect.DeepEqual(gotKeep, wantKeep) {
		t.Fatalf("kept archives = %#v, want %#v", gotKeep, wantKeep)
	}
	gotDrop := archiveIDs(decision.Drop)
	wantDrop := []string{"expired"}
	if !reflect.DeepEqual(gotDrop, wantDrop) {
		t.Fatalf("dropped archives = %#v, want %#v", gotDrop, wantDrop)
	}
}

func archiveIDs(archives []BackupArchive) []string {
	ids := make([]string, 0, len(archives))
	for _, archive := range archives {
		ids = append(ids, archive.ID)
	}
	sort.Strings(ids)
	return ids
}

func mustTime(value string) time.Time {
	timeValue, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return timeValue
}
