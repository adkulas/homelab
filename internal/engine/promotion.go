package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adkulas/homelab/internal/config"
)

const promotionSchemaVersion = "homelab.media-stack/promotion/v1alpha1"

type PromoteRequest struct {
	plan             PlanRequest
	verificationPath string
	backupPath       string
}

type PromoteReport struct {
	SchemaVersion string `json:"schemaVersion"`
	Environment   string `json:"environment"`
	Preview       string `json:"preview"`
}

func NewPromoteRequest(workingDirectory, environment, configPath, verificationPath, backupPath string) (PromoteRequest, error) {
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	if err != nil {
		return PromoteRequest{}, err
	}
	if verificationPath != "" && !filepath.IsAbs(verificationPath) {
		verificationPath = filepath.Join(workingDirectory, verificationPath)
	}
	if backupPath != "" && !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(workingDirectory, backupPath)
	}
	return PromoteRequest{plan: plan, verificationPath: verificationPath, backupPath: backupPath}, nil
}

func (engine localEngine) Promote(ctx context.Context, request PromoteRequest) (PromoteReport, error) {
	if err := ctx.Err(); err != nil {
		return PromoteReport{}, err
	}
	declared, err := request.plan.load()
	if err != nil {
		return PromoteReport{}, err
	}
	if err := declared.ValidateEnvironment(request.plan.environment); err != nil {
		return PromoteReport{}, err
	}
	verificationFile, err := os.ReadFile(request.verificationPath)
	if err != nil {
		return PromoteReport{}, fmt.Errorf("read verification artifact: %w", err)
	}
	var verification VerifyReport
	if err := json.Unmarshal(verificationFile, &verification); err != nil {
		return PromoteReport{}, fmt.Errorf("decode verification artifact: %w", err)
	}
	if verification.SchemaVersion != verifySchemaVersion {
		return PromoteReport{}, fmt.Errorf("verification artifact schema version %q is unsupported", verification.SchemaVersion)
	}
	if verification.Environment != "staging" {
		return PromoteReport{}, fmt.Errorf("promotion requires a staging verification artifact")
	}
	if verification.ConfigDigest != request.plan.configurationDigest() || verification.VersionsDigest != fileDigest(request.plan.versionsPath) {
		return PromoteReport{}, fmt.Errorf("verification artifact does not match the current Declared Configuration and checked-in versions")
	}
	backupFile, err := os.ReadFile(request.backupPath)
	if err != nil {
		return PromoteReport{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var backup BackupReport
	if err := json.Unmarshal(backupFile, &backup); err != nil {
		return PromoteReport{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if backup.SchemaVersion != backupSchemaVersion {
		return PromoteReport{}, fmt.Errorf("backup manifest schema version %q is unsupported", backup.SchemaVersion)
	}
	if backup.Scope != nil {
		expected := declared.Spec.Environments["staging"].SecretScope
		if expected == nil || *expected != *backup.Scope {
			return PromoteReport{}, fmt.Errorf("backup ownership does not match selected Staging Environment")
		}
	}
	if backup.Environment != "staging" {
		return PromoteReport{}, fmt.Errorf("promotion requires a Staging backup")
	}
	if seriesAutomationEnabled(declared.Spec.Environments["production"]) {
		if err := validateCompanionPromotion(declared, verification, backup); err != nil {
			return PromoteReport{}, err
		}
	}
	preview := fmt.Sprintf("promote the verified Staging change to Production using backup %s", request.backupPath)
	return PromoteReport{SchemaVersion: promotionSchemaVersion, Environment: request.plan.environment, Preview: preview}, nil
}

func validateCompanionPromotion(declared config.MediaStack, verification VerifyReport, backup BackupReport) error {
	if !seriesAutomationEnabled(declared.Spec.Environments["staging"]) || verification.Suite != "promotion" || verification.Failed() {
		return fmt.Errorf("series companion Promotion requires successful enabled Staging request and recovery verification")
	}
	passed := map[string]bool{}
	for _, diagnostic := range verification.Diagnostics {
		if diagnostic.Status == "pass" {
			passed[diagnostic.Code] = true
		}
	}
	for _, code := range []string{"VERIFY_COMPANION_POLICY_CONVERGED", "VERIFY_EPISODE_SEARCH_DISPATCHED", "VERIFY_SERIES_RECOVERY_PROVEN", "VERIFY_SERIES_REQUESTED", "VERIFY_SERIES_EPISODE_ACQUIRED", "VERIFY_SERIES_EPISODE_HARDLINKED", "VERIFY_SERIES_EPISODE_PLAYBACK_READY"} {
		if !passed[code] {
			return fmt.Errorf("series companion Promotion evidence is missing %s", code)
		}
	}
	project := declared.Spec.Environments["staging"].ProjectName
	covered := map[string]bool{}
	for _, service := range backup.Services {
		if service.Volume == service.Name+"-config" && service.DockerVolume == project+"_"+service.Volume {
			covered[service.Name] = true
		}
	}
	if !covered["houndarr"] || !covered["cleanuparr"] {
		return fmt.Errorf("Staging backup must include Environment-scoped companion state for Houndarr and Cleanuparr")
	}
	return nil
}
