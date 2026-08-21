package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

var ErrRestoreConfirmationRequired = errors.New("restore requires --confirm")

type RestoreRequest struct {
	plan            PlanRequest
	backupPath      string
	credentialsPath string
	confirm         bool
	asRestoreDrill  bool
}

type RestoreReport struct {
	SchemaVersion        string   `json:"schemaVersion"`
	Environment          string   `json:"environment"`
	ProjectName          string   `json:"projectName"`
	BackupPath           string   `json:"backupPath"`
	CredentialsPath      string   `json:"credentialsPath,omitempty"`
	Preview              string   `json:"preview"`
	RestoreDrill         bool     `json:"restoreDrill"`
	SourceEnvironment    string   `json:"sourceEnvironment"`
	AcquisitionDisabled  bool     `json:"acquisitionDisabled"`
	IntegrationsGated    bool     `json:"integrationsGated"`
	Services             []string `json:"services"`
	Completed            bool     `json:"completed"`
	SafetyBackupPath     string   `json:"safetyBackupPath,omitempty"`
	OperationJournalPath string   `json:"operationJournalPath,omitempty"`
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
	declared, err := config.Load(request.plan.configPath)
	if err != nil {
		return RestoreReport{}, err
	}
	if err := declared.ValidateEnvironment(request.plan.environment); err != nil {
		return RestoreReport{}, err
	}
	plan, err := engine.Plan(ctx, request.plan)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("plan restore coverage: %w", err)
	}
	environment := declared.Spec.Environments[request.plan.environment]
	sources, err := backupSources(plan.Compose(), environment.ProjectName)
	if err != nil {
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
	if backup.CLIContractVersion != backupCLIContractVersion || !backup.Complete {
		return RestoreReport{}, fmt.Errorf("backup is not a complete compatible %s archive", request.plan.environment)
	}
	if backup.Environment != request.plan.environment && !request.asRestoreDrill {
		return RestoreReport{}, fmt.Errorf("restore requires a backup from the %s Environment", request.plan.environment)
	}
	if !request.asRestoreDrill && backup.ProjectName != environment.ProjectName {
		return RestoreReport{}, fmt.Errorf("restore backup project %q does not match %q", backup.ProjectName, environment.ProjectName)
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
	serviceNames := make([]string, 0, len(backup.Services))
	if err := validateRestoreCoverage(backup.Services, sources, request.asRestoreDrill); err != nil {
		return RestoreReport{}, err
	}
	if err := verifyBackupArchives(filepath.Dir(request.backupPath), backup.Services); err != nil {
		return RestoreReport{}, fmt.Errorf("verify backup checksums: %w", err)
	}
	for _, service := range backup.Services {
		serviceNames = append(serviceNames, service.Name)
	}
	preview := fmt.Sprintf("replace %s Environment state from %s backup", request.plan.environment, backup.Environment)
	if request.asRestoreDrill {
		preview = fmt.Sprintf("restore drill: replace %s Environment state from %s backup with acquisition disabled, integrations gated, and credentials overridden from %s", request.plan.environment, backup.Environment, filepath.Base(request.credentialsPath))
	}
	report := RestoreReport{
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
	}
	if !request.confirm {
		return report, ErrRestoreConfirmationRequired
	}
	if request.asRestoreDrill {
		return report, nil
	}
	return engine.executeRestore(ctx, request, backup, sources, plan.Compose(), report)
}

func validateRestoreCoverage(services []BackupService, sources []backupSource, restoreDrill bool) error {
	if len(services) != len(sources) {
		return fmt.Errorf("backup does not provide complete mutable service coverage: found %d services, require %d", len(services), len(sources))
	}
	covered := make(map[string]BackupService, len(services))
	for _, service := range services {
		if _, duplicate := covered[service.Name]; duplicate {
			return fmt.Errorf("backup does not provide complete mutable service coverage: service %q appears more than once", service.Name)
		}
		covered[service.Name] = service
	}
	for _, source := range sources {
		service, ok := covered[source.serviceName]
		if !ok {
			return fmt.Errorf("backup does not provide complete mutable service coverage: service %q is missing", source.serviceName)
		}
		archivePath := filepath.Clean(filepath.FromSlash(service.ArchivePath))
		if service.ArchivePath == "" || filepath.IsAbs(archivePath) || archivePath == ".." || strings.HasPrefix(archivePath, ".."+string(filepath.Separator)) {
			return fmt.Errorf("backup service %q has unsafe archive path %q", source.serviceName, service.ArchivePath)
		}
		if service.Volume != source.volumeName || service.MountPath != source.mountPath || service.Image != source.image {
			return fmt.Errorf("backup service %q is incompatible with the rendered mutable volume", source.serviceName)
		}
		if !restoreDrill && service.DockerVolume != source.dockerVolume {
			return fmt.Errorf("backup service %q is incompatible with the rendered mutable volume", source.serviceName)
		}
	}
	return nil
}

func (report RestoreReport) HumanSummary() string {
	if report.Completed {
		return fmt.Sprintf("Restored %s Environment state. Safety backup: %s. Operation journal: %s.", report.Environment, report.SafetyBackupPath, report.OperationJournalPath)
	}
	return report.Preview
}
