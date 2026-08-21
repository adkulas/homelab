package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

const backupSchemaVersion = "homelab.media-stack/backup/v1alpha1"

type BackupRequest struct {
	plan          PlanRequest
	label         string
	protect       bool
	generatedAt   time.Time
	skipRetention bool
}

type BackupArchive struct {
	ID          string
	GeneratedAt time.Time
	Protected   bool
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

func NewBackupRequest(workingDirectory, environment, configPath, label string, protect bool, generatedAt time.Time) (BackupRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return BackupRequest{}, err
	}
	return BackupRequest{plan: plan, label: label, protect: protect, generatedAt: generatedAt}, nil
}

func (engine localEngine) Backup(ctx context.Context, request BackupRequest) (BackupReport, error) {
	return engine.executeBackup(ctx, request)
}

func checksum(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func ApplyRetention(policy config.BackupRetention, archives []BackupArchive, now time.Time) RetentionDecision {
	now = now.UTC()
	kept := make(map[string]BackupArchive)
	for _, archive := range archives {
		if archive.Protected || archive.GeneratedAt.After(now) {
			kept[archive.ID] = archive
		}
	}

	selectNewest := func(start, end time.Time, limit int, bucket func(time.Time) string) {
		if limit <= 0 {
			return
		}
		var ordered []BackupArchive
		for _, archive := range archives {
			if archive.Protected || archive.GeneratedAt.Before(start) || !archive.GeneratedAt.Before(end) {
				continue
			}
			ordered = append(ordered, archive)
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].GeneratedAt.Equal(ordered[j].GeneratedAt) {
				return ordered[i].ID > ordered[j].ID
			}
			return ordered[i].GeneratedAt.After(ordered[j].GeneratedAt)
		})
		seen := make(map[string]bool)
		for _, archive := range ordered {
			key := bucket(archive.GeneratedAt)
			if seen[key] {
				continue
			}
			seen[key] = true
			kept[archive.ID] = archive
			if len(seen) == limit {
				break
			}
		}
	}

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dailyStart := now
	if policy.Daily > 0 {
		dailyStart = dayStart.AddDate(0, 0, 1-policy.Daily)
		selectNewest(dailyStart, now.Add(time.Nanosecond), policy.Daily, func(value time.Time) string {
			return value.UTC().Format("2006-01-02")
		})
	}
	weeklyEnd := startOfISOWeek(dailyStart)
	weeklyStart := weeklyEnd.AddDate(0, 0, -7*policy.Weekly)
	selectNewest(weeklyStart, weeklyEnd, policy.Weekly, func(value time.Time) string {
		year, week := value.UTC().ISOWeek()
		return fmt.Sprintf("%04d-%02d", year, week)
	})
	monthlyEnd := time.Date(weeklyStart.Year(), weeklyStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthlyStart := monthlyEnd.AddDate(0, -policy.Monthly, 0)
	selectNewest(monthlyStart, monthlyEnd, policy.Monthly, func(value time.Time) string {
		return value.UTC().Format("2006-01")
	})

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

func startOfISOWeek(value time.Time) time.Time {
	value = value.UTC()
	dayStart := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(dayStart.Weekday()) + 6) % 7
	return dayStart.AddDate(0, 0, -offset)
}
