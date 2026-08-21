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
	declared, err := config.Load(request.plan.configPath)
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
	if verification.ConfigDigest != fileDigest(request.plan.configPath) || verification.VersionsDigest != fileDigest(request.plan.versionsPath) {
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
	if backup.Environment != "staging" {
		return PromoteReport{}, fmt.Errorf("promotion requires a Staging backup")
	}
	preview := fmt.Sprintf("promote the verified Staging change to Production using backup %s", request.backupPath)
	return PromoteReport{SchemaVersion: promotionSchemaVersion, Environment: request.plan.environment, Preview: preview}, nil
}
