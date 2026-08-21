package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

const backupSchemaVersion = "homelab.media-stack/backup/v1alpha1"

type BackupRequest struct {
	plan    PlanRequest
	label   string
	protect bool
}

type BackupArchive struct {
	ID          string
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
	SchemaVersion      string          `json:"schemaVersion"`
	CLIContractVersion string          `json:"cliContractVersion"`
	ID                 string          `json:"id"`
	ManifestPath       string          `json:"manifestPath"`
	Environment        string          `json:"environment"`
	ProjectName        string          `json:"projectName"`
	Label              string          `json:"label,omitempty"`
	Protected          bool            `json:"protected"`
	ConfigDigest       string          `json:"configDigest"`
	VersionDigest      string          `json:"versionDigest"`
	GeneratedAt        time.Time       `json:"generatedAt"`
	Complete           bool            `json:"complete"`
	Services           []BackupService `json:"services"`
}

type backupConsistencyMethod string

const (
	readOnlyVolumeArchive         backupConsistencyMethod = "read-only-volume-archive"
	quiescedReadOnlyVolumeArchive backupConsistencyMethod = "compose-stop+read-only-volume-archive"
)

type BackupService struct {
	Name              string                  `json:"name"`
	Volume            string                  `json:"volume"`
	DockerVolume      string                  `json:"dockerVolume"`
	MountPath         string                  `json:"mountPath"`
	Image             string                  `json:"image"`
	ArchivePath       string                  `json:"archivePath"`
	ChecksumSHA256    string                  `json:"checksumSHA256"`
	ConsistencyMethod backupConsistencyMethod `json:"consistencyMethod"`
	SizeBytes         int64                   `json:"sizeBytes"`
}

func NewBackupRequest(workingDirectory, environment, configPath, label string, protect bool) (BackupRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return BackupRequest{}, err
	}
	return BackupRequest{plan: plan, label: label, protect: protect}, nil
}

func (engine localEngine) Backup(ctx context.Context, request BackupRequest) (BackupReport, error) {
	return engine.executeBackup(ctx, request)
}

func checksum(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func ApplyRetention(policy RetentionPolicy, archives []BackupArchive) RetentionDecision {
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
