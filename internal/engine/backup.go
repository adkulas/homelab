package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

const backupSchemaVersion = "homelab.media-stack/backup/v1alpha1"

type BackupRequest struct {
	plan    PlanRequest
	label   string
	protect bool
}

type BackupArchive struct {
	ID         string
	GeneratedAt time.Time
	Protected   bool
}

type RetentionPolicy struct {
	Daily   int
	Weekly  int
	Monthly int
}

type RetentionDecision struct {
	Keep []BackupArchive
	Drop []BackupArchive
}

type BackupReport struct {
	SchemaVersion string          `json:"schemaVersion"`
	Environment   string          `json:"environment"`
	ProjectName   string          `json:"projectName"`
	Label         string          `json:"label,omitempty"`
	Protected     bool            `json:"protected"`
	ConfigDigest  string          `json:"configDigest"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	Services      []BackupService `json:"services"`
}

type BackupService struct {
	Name   string   `json:"name"`
	Volume string   `json:"volume"`
	Paths  []string `json:"paths"`
}

func NewBackupRequest(workingDirectory, environment, configPath, label string, protect bool) (BackupRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return BackupRequest{}, err
	}
	return BackupRequest{plan: plan, label: label, protect: protect}, nil
}

func (engine localEngine) Backup(ctx context.Context, request BackupRequest) (BackupReport, error) {
	if err := ctx.Err(); err != nil {
		return BackupReport{}, err
	}
	declared, err := config.Load(request.plan.configPath)
	if err != nil {
		return BackupReport{}, err
	}
	if err := declared.ValidateEnvironment(request.plan.environment); err != nil {
		return BackupReport{}, err
	}
	data, err := os.ReadFile(request.plan.configPath)
	if err != nil {
		return BackupReport{}, fmt.Errorf("read Declared Configuration: %w", err)
	}
	environment := declared.Spec.Environments[request.plan.environment]
	report := BackupReport{
		SchemaVersion: backupSchemaVersion,
		Environment:   request.plan.environment,
		ProjectName:   environment.ProjectName,
		Label:         request.label,
		Protected:     request.protect,
		ConfigDigest:  checksum(data),
		GeneratedAt:   time.Now().UTC().Truncate(time.Second),
		Services: []BackupService{
			{Name: "qbittorrent", Volume: "qbittorrent-config", Paths: []string{"/config", filepath.Join(environment.DataRoot, "torrents")}},
			{Name: "prowlarr", Volume: "prowlarr-config", Paths: []string{"/config"}},
			{Name: "sonarr", Volume: "sonarr-config", Paths: []string{"/config", environment.DataRoot}},
			{Name: "radarr", Volume: "radarr-config", Paths: []string{"/config", environment.DataRoot}},
			{Name: "profilarr", Volume: "profilarr-config", Paths: []string{"/config"}},
			{Name: "jellyfin", Volume: "jellyfin-config", Paths: []string{"/config", filepath.Join(environment.DataRoot, "media")}},
			{Name: "seerr", Volume: "seerr-config", Paths: []string{"/app/config"}},
		},
	}
	return report, nil
}

func checksum(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func ApplyRetention(policy RetentionPolicy, archives []BackupArchive, now time.Time) RetentionDecision {
	_ = now
	kept := make(map[string]BackupArchive)
	remaining := make([]BackupArchive, 0, len(archives))
	for _, archive := range archives {
		if archive.Protected {
			kept[archive.ID] = archive
			continue
		}
		remaining = append(remaining, archive)
	}

	selectNewest := func(candidates []BackupArchive, limit int, bucket func(time.Time) string) ([]BackupArchive, []BackupArchive) {
		if limit <= 0 || len(candidates) == 0 {
			return nil, candidates
		}
		ordered := append([]BackupArchive(nil), candidates...)
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].GeneratedAt.Equal(ordered[j].GeneratedAt) {
				return ordered[i].ID > ordered[j].ID
			}
			return ordered[i].GeneratedAt.After(ordered[j].GeneratedAt)
		})
		seen := make(map[string]bool)
		selected := make([]BackupArchive, 0, limit)
		for _, archive := range ordered {
			key := bucket(archive.GeneratedAt)
			if seen[key] {
				continue
			}
			seen[key] = true
			selected = append(selected, archive)
			if len(selected) == limit {
				break
			}
		}
		selectedIDs := make(map[string]struct{}, len(selected))
		for _, archive := range selected {
			selectedIDs[archive.ID] = struct{}{}
			kept[archive.ID] = archive
		}
		var next []BackupArchive
		for _, archive := range candidates {
			if _, ok := selectedIDs[archive.ID]; ok {
				continue
			}
			next = append(next, archive)
		}
		return selected, next
	}

	remaining = func(input []BackupArchive) []BackupArchive {
		_, output := selectNewest(input, policy.Daily, func(t time.Time) string { return t.UTC().Format("2006-01-02") })
		return output
	}(remaining)
	remaining = func(input []BackupArchive) []BackupArchive {
		_, output := selectNewest(input, policy.Weekly, func(t time.Time) string {
			year, week := t.UTC().ISOWeek()
			return fmt.Sprintf("%04d-%02d", year, week)
		})
		return output
	}(remaining)
	remaining = func(input []BackupArchive) []BackupArchive {
		_, output := selectNewest(input, policy.Monthly, func(t time.Time) string { return t.UTC().Format("2006-01") })
		return output
	}(remaining)

	var decision RetentionDecision
	for _, archive := range archives {
		if _, ok := kept[archive.ID]; ok {
			decision.Keep = append(decision.Keep, archive)
			continue
		}
		decision.Drop = append(decision.Drop, archive)
	}
	return decision
}
