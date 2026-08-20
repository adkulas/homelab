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
	plan            PlanRequest
	backupPath      string
	credentialsPath string
	confirm         bool
	asRestoreDrill  bool
}

type RestoreReport struct {
	SchemaVersion       string   `json:"schemaVersion"`
	Environment         string   `json:"environment"`
	ProjectName         string   `json:"projectName"`
	BackupPath          string   `json:"backupPath"`
	CredentialsPath     string   `json:"credentialsPath,omitempty"`
	Preview             string   `json:"preview"`
	RestoreDrill        bool     `json:"restoreDrill"`
	SourceEnvironment   string   `json:"sourceEnvironment"`
	AcquisitionDisabled bool     `json:"acquisitionDisabled"`
	IntegrationsGated   bool     `json:"integrationsGated"`
	Services            []string `json:"services"`
}

func NewRestoreRequest(workingDirectory, environment, configPath, backupPath, credentialsPath string, confirm, asRestoreDrill bool) (RestoreRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return RestoreRequest{}, err
	}
	if backupPath != "" && !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(workingDirectory, backupPath)
	}
	if credentialsPath != "" && !filepath.IsAbs(credentialsPath) {
		credentialsPath = filepath.Join(workingDirectory, credentialsPath)
	}
	return RestoreRequest{plan: plan, backupPath: backupPath, credentialsPath: credentialsPath, confirm: confirm, asRestoreDrill: asRestoreDrill}, nil
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
	if request.asRestoreDrill {
		if request.plan.environment != "staging" {
			return RestoreReport{}, fmt.Errorf("restore drill requires the staging Environment")
		}
		if backup.Environment != "production" {
			return RestoreReport{}, fmt.Errorf("restore drill requires a production backup")
		}
		if request.credentialsPath == "" {
			return RestoreReport{}, fmt.Errorf("restore drill requires --credentials")
		}
	}
	environment := declared.Spec.Environments[request.plan.environment]
	serviceNames := make([]string, 0, len(backup.Services))
	for _, service := range backup.Services {
		serviceNames = append(serviceNames, service.Name)
	}
	preview := fmt.Sprintf("replace %s Environment state from %s backup", request.plan.environment, backup.Environment)
	if request.asRestoreDrill {
		preview = fmt.Sprintf("restore drill: replace %s Environment state from %s backup with acquisition disabled, integrations gated, and credentials overridden from %s", request.plan.environment, backup.Environment, filepath.Base(request.credentialsPath))
	}
	return RestoreReport{
		SchemaVersion:       backupSchemaVersion,
		Environment:         request.plan.environment,
		ProjectName:         environment.ProjectName,
		BackupPath:          request.backupPath,
		CredentialsPath:     request.credentialsPath,
		Preview:             preview,
		RestoreDrill:        request.asRestoreDrill,
		SourceEnvironment:   backup.Environment,
		AcquisitionDisabled: request.asRestoreDrill,
		IntegrationsGated:   request.asRestoreDrill,
		Services:            serviceNames,
	}, nil
}

func (report RestoreReport) HumanSummary() string {
	return fmt.Sprintf("Prepared restore of %s backup into the %s Environment.", strings.TrimSpace(report.BackupPath), report.Environment)
}
