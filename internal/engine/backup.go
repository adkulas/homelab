package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

const backupSchemaVersion = "homelab.media-stack/backup/v1alpha1"

type BackupRequest struct {
	plan    PlanRequest
	label   string
	protect bool
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
