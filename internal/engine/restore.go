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
	plan                PlanRequest
	backupPath          string
	credentialsPath     string
	confirm             bool
	asRestoreDrill      bool
	confirmIntegrations bool
}

type RestoreReport struct {
	SchemaVersion        string   `json:"schemaVersion"`
	Environment          string   `json:"environment"`
	ProjectName          string   `json:"projectName"`
	BackupPath           string   `json:"backupPath"`
	Preview              string   `json:"preview"`
	RestoreDrill         bool     `json:"restoreDrill"`
	SourceEnvironment    string   `json:"sourceEnvironment"`
	AcquisitionDisabled  bool     `json:"acquisitionDisabled"`
	IntegrationsGated    bool     `json:"integrationsGated"`
	Services             []string `json:"services"`
	ExcludedServices     []string `json:"excludedServices,omitempty"`
	StartedServices      []string `json:"startedServices,omitempty"`
	credentialsSHA256    string
	PreviewPath          string `json:"previewPath,omitempty"`
	Completed            bool   `json:"completed"`
	SafetyBackupPath     string `json:"safetyBackupPath,omitempty"`
	OperationJournalPath string `json:"operationJournalPath,omitempty"`
}

func NewRestoreRequest(workingDirectory, environment, configPath, backupPath, credentialsPath string, confirm, asRestoreDrill, confirmIntegrations bool) (RestoreRequest, error) {
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
	return RestoreRequest{plan: plan, backupPath: backupPath, credentialsPath: credentialsPath, confirm: confirm, asRestoreDrill: asRestoreDrill, confirmIntegrations: confirmIntegrations}, nil
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
	if request.confirmIntegrations {
		if request.plan.environment != "staging" || !request.asRestoreDrill {
			return RestoreReport{}, fmt.Errorf("integration confirmation requires --environment staging and --as-restore-drill")
		}
		if request.credentialsPath == "" {
			return RestoreReport{}, fmt.Errorf("integration confirmation requires --credentials")
		}
		credentialsContents, err := os.ReadFile(request.credentialsPath)
		if err != nil {
			return RestoreReport{}, fmt.Errorf("read Restore Drill confirmation credentials: %w", err)
		}
		if _, err := decryptEnvironmentSecrets(ctx, request.credentialsPath); err != nil {
			return RestoreReport{}, fmt.Errorf("validate Restore Drill confirmation credentials: %w", err)
		}
		if err := confirmRestoreDrillGate(environment.BackupRoot, environment.ProjectName, credentialsContents); err != nil {
			return RestoreReport{}, err
		}
		return RestoreReport{
			SchemaVersion:     backupSchemaVersion,
			Environment:       request.plan.environment,
			ProjectName:       environment.ProjectName,
			Preview:           "confirmed Restore Drill integrations for the next apply",
			RestoreDrill:      true,
			IntegrationsGated: false,
		}, nil
	}
	sources, err := backupSources(plan.Compose(), environment.ProjectName)
	if err != nil {
		return RestoreReport{}, err
	}
	if err := recoverInterruptedRestores(ctx, environment.BackupRoot, request.plan.environment, environment.ProjectName, sources); err != nil {
		return RestoreReport{}, fmt.Errorf("recover interrupted restore: %w", err)
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
	var drillSecrets environmentSecrets
	var stagingSecrets environmentSecrets
	var sourceSecrets environmentSecrets
	var credentialsContents []byte
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
		credentialsContents, err = os.ReadFile(request.credentialsPath)
		if err != nil {
			return RestoreReport{}, fmt.Errorf("read restore drill credentials: %w", err)
		}
		drillSecrets, err = decryptEnvironmentSecrets(ctx, request.credentialsPath)
		if err != nil {
			return RestoreReport{}, fmt.Errorf("validate restore drill credentials: %w", err)
		}
		stagingSecrets, err = decryptSelectedEnvironmentSecrets(ctx, request.plan.configPath, environment)
		if err != nil {
			return RestoreReport{}, fmt.Errorf("validate staging Environment credentials: %w", err)
		}
		sourceEnvironment, ok := declared.Spec.Environments[backup.Environment]
		if !ok {
			return RestoreReport{}, fmt.Errorf("restore drill source Environment %q is not declared", backup.Environment)
		}
		sourceSecrets, err = decryptSelectedEnvironmentSecrets(ctx, request.plan.configPath, sourceEnvironment)
		if err != nil {
			return RestoreReport{}, fmt.Errorf("validate production Environment credentials: %w", err)
		}
	}
	serviceNames := make([]string, 0, len(backup.Services))
	if err := validateRestoreCoverage(backup.Services, sources, request.asRestoreDrill); err != nil {
		return RestoreReport{}, err
	}
	if err := verifyBackupArchives(filepath.Dir(request.backupPath), backup.Services); err != nil {
		return RestoreReport{}, fmt.Errorf("verify backup checksums: %w", err)
	}
	excludedServices := []string(nil)
	if request.asRestoreDrill {
		excludedServices = []string{"profilarr"}
		sources = excludeRestoreSources(sources, excludedServices)
	}
	for _, source := range sources {
		serviceNames = append(serviceNames, source.serviceName)
	}
	preview := fmt.Sprintf("replace %s Environment state from %s backup", request.plan.environment, backup.Environment)
	if request.asRestoreDrill {
		preview = fmt.Sprintf("restore drill: replace %s Environment state from %s backup with acquisition disabled, integrations gated, rotated credentials, and Production Profilarr excluded", request.plan.environment, backup.Environment)
	}
	report := RestoreReport{
		SchemaVersion:       backupSchemaVersion,
		Environment:         request.plan.environment,
		ProjectName:         environment.ProjectName,
		BackupPath:          request.backupPath,
		Preview:             preview,
		RestoreDrill:        request.asRestoreDrill,
		SourceEnvironment:   backup.Environment,
		AcquisitionDisabled: request.asRestoreDrill,
		IntegrationsGated:   request.asRestoreDrill,
		Services:            serviceNames,
		ExcludedServices:    excludedServices,
	}
	if request.asRestoreDrill {
		report.credentialsSHA256 = checksum(credentialsContents)
	}
	previewPath, err := prepareOrConfirmRestorePreview(environment.BackupRoot, backupFile, report, request.confirm)
	if err != nil {
		return RestoreReport{}, err
	}
	report.PreviewPath = previewPath
	if !request.confirm {
		return report, ErrRestoreConfirmationRequired
	}
	jellyfinURL := "http://" + environmentAddress(declared.Spec.Defaults.LANBindAddress, environment.Ports.Jellyfin)
	gateCreated := false
	if request.asRestoreDrill {
		if err := writeRestoreDrillGate(environment.BackupRoot, environment.ProjectName, credentialsContents); err != nil {
			return RestoreReport{}, err
		}
		gateCreated = true
	}
	result, err := engine.executeRestore(ctx, request, backup, sources, plan.Compose(), report, drillSecrets, stagingSecrets, sourceSecrets, jellyfinURL)
	if err != nil && gateCreated {
		if cleanupErr := completeRestoreDrillGate(environment.BackupRoot); cleanupErr != nil {
			return RestoreReport{}, errors.Join(err, cleanupErr)
		}
	}
	return result, err
}

func excludeRestoreSources(sources []backupSource, excluded []string) []backupSource {
	excludedSet := make(map[string]bool, len(excluded))
	for _, name := range excluded {
		excludedSet[name] = true
	}
	selected := make([]backupSource, 0, len(sources)-len(excluded))
	for _, source := range sources {
		if !excludedSet[source.serviceName] {
			selected = append(selected, source)
		}
	}
	return selected
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
