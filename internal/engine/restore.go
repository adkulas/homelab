package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

type RestoreRequest struct {
	plan           PlanRequest
	backupPath     string
	confirm        bool
	asRestoreDrill bool
}

type RestoreReport struct {
	SchemaVersion string   `json:"schemaVersion"`
	Environment   string   `json:"environment"`
	ProjectName   string   `json:"projectName"`
	BackupPath    string   `json:"backupPath"`
	Preview       string   `json:"preview"`
	Services      []string `json:"services"`
}

func NewRestoreRequest(workingDirectory, environment, configPath, backupPath string, confirm, asRestoreDrill bool) (RestoreRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return RestoreRequest{}, err
	}
	if backupPath != "" && !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(workingDirectory, backupPath)
	}
	return RestoreRequest{plan: plan, backupPath: backupPath, confirm: confirm, asRestoreDrill: asRestoreDrill}, nil
}

func (engine localEngine) Restore(ctx context.Context, request RestoreRequest) (RestoreReport, error) {
	if err := ctx.Err(); err != nil {
		return RestoreReport{}, err
	}
	if !request.confirm {
		return RestoreReport{}, fmt.Errorf("restore requires --confirm")
	}
	declared, err := config.Load(request.plan.configPath)
	if err != nil {
		return RestoreReport{}, err
	}
	if err := declared.ValidateEnvironment(request.plan.environment); err != nil {
		return RestoreReport{}, err
	}
	backupFile, err := os.ReadFile(request.backupPath)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var backup BackupReport
	if err := json.Unmarshal(backupFile, &backup); err != nil {
		return RestoreReport{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if backup.SchemaVersion != backupSchemaVersion {
		return RestoreReport{}, fmt.Errorf("backup manifest schema version %q is unsupported", backup.SchemaVersion)
	}
	if backup.Environment != request.plan.environment && !request.asRestoreDrill {
		return RestoreReport{}, fmt.Errorf("restore requires a backup from the %s Environment", request.plan.environment)
	}
	environment := declared.Spec.Environments[request.plan.environment]
	serviceNames := make([]string, 0, len(backup.Services))
	for _, service := range backup.Services {
		serviceNames = append(serviceNames, service.Name)
	}
	return RestoreReport{
		SchemaVersion: backupSchemaVersion,
		Environment:   request.plan.environment,
		ProjectName:   environment.ProjectName,
		BackupPath:    request.backupPath,
		Preview:       fmt.Sprintf("replace %s Environment state from %s backup", request.plan.environment, backup.Environment),
		Services:      serviceNames,
	}, nil
}

func (report RestoreReport) HumanSummary() string {
	return fmt.Sprintf("Prepared restore of %s backup into the %s Environment.", strings.TrimSpace(report.BackupPath), report.Environment)
}
