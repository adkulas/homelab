package engine

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

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

	decision := ApplyRetention(config.BackupRetention{Daily: 1, Weekly: 1, Monthly: 1}, archives, mustTime("2026-08-20T23:59:59Z"))

	gotKeep := archiveIDs(decision.Keep)
	wantKeep := []string{"2026-07-31", "2026-08-13", "2026-08-20-b"}
	if !reflect.DeepEqual(gotKeep, wantKeep) {
		t.Fatalf("kept archives = %#v, want %#v", gotKeep, wantKeep)
	}
	gotDrop := archiveIDs(decision.Drop)
	wantDrop := []string{"2026-05-15", "2026-06-30", "2026-08-19", "2026-08-20-a"}
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

	decision := ApplyRetention(config.BackupRetention{}, archives, mustTime("2026-08-20T23:59:59Z"))

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
