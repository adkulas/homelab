package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const restoreDrillGateSchema = "homelab.media-stack/restore-drill-gate/v1alpha1"

type restoreDrillGate struct {
	SchemaVersion     string `json:"schemaVersion"`
	ProjectName       string `json:"projectName"`
	CredentialsSHA256 string `json:"credentialsSHA256"`
	Confirmed         bool   `json:"confirmed"`
}

func restoreDrillGatePaths(backupRoot string) (gatePath, credentialsPath string) {
	directory := filepath.Join(backupRoot, ".restore-drill")
	return filepath.Join(directory, "gate.json"), filepath.Join(directory, "credentials.sops.yaml")
}

func writeRestoreDrillGate(backupRoot, projectName string, encryptedCredentials []byte) error {
	gatePath, credentialsPath := restoreDrillGatePaths(backupRoot)
	if err := os.MkdirAll(filepath.Dir(gatePath), 0o700); err != nil {
		return fmt.Errorf("create Restore Drill gate directory: %w", err)
	}
	if err := writeSecretAtomic(credentialsPath, encryptedCredentials); err != nil {
		return fmt.Errorf("preserve encrypted Restore Drill credentials: %w", err)
	}
	gate := restoreDrillGate{
		SchemaVersion:     restoreDrillGateSchema,
		ProjectName:       projectName,
		CredentialsSHA256: checksum(encryptedCredentials),
	}
	if err := writeRestoreDrillGateState(gatePath, gate); err != nil {
		_ = os.Remove(credentialsPath)
		return err
	}
	return nil
}

func writeRestoreDrillGateState(path string, gate restoreDrillGate) error {
	contents, err := json.MarshalIndent(gate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Restore Drill gate: %w", err)
	}
	contents = append(contents, '\n')
	if err := writeSecretAtomic(path, contents); err != nil {
		return fmt.Errorf("write Restore Drill gate: %w", err)
	}
	return nil
}

func readRestoreDrillGate(backupRoot, projectName string) (restoreDrillGate, string, error) {
	gatePath, credentialsPath := restoreDrillGatePaths(backupRoot)
	contents, err := os.ReadFile(gatePath)
	if err != nil {
		return restoreDrillGate{}, "", err
	}
	var gate restoreDrillGate
	if err := json.Unmarshal(contents, &gate); err != nil {
		return restoreDrillGate{}, "", fmt.Errorf("decode Restore Drill gate: %w", err)
	}
	if gate.SchemaVersion != restoreDrillGateSchema || gate.ProjectName != projectName {
		return restoreDrillGate{}, "", fmt.Errorf("Restore Drill gate does not match project %q", projectName)
	}
	return gate, credentialsPath, nil
}

func confirmRestoreDrillGate(backupRoot, projectName string, encryptedCredentials []byte) error {
	gate, _, err := readRestoreDrillGate(backupRoot, projectName)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no completed Restore Drill has gated integrations")
		}
		return err
	}
	if gate.CredentialsSHA256 != checksum(encryptedCredentials) {
		return fmt.Errorf("integration confirmation credentials do not match the completed Restore Drill")
	}
	gate.Confirmed = true
	gatePath, _ := restoreDrillGatePaths(backupRoot)
	return writeRestoreDrillGateState(gatePath, gate)
}

func restoreDrillSecretsForApply(ctx context.Context, backupRoot, projectName string) (environmentSecrets, bool, error) {
	gate, credentialsPath, err := readRestoreDrillGate(backupRoot, projectName)
	if os.IsNotExist(err) {
		return environmentSecrets{}, false, nil
	}
	if err != nil {
		return environmentSecrets{}, false, err
	}
	if !gate.Confirmed {
		return environmentSecrets{}, false, fmt.Errorf("Restore Drill integrations require explicit confirmation with media-stack restore --confirm-integrations")
	}
	secrets, err := decryptEnvironmentSecrets(ctx, credentialsPath)
	if err != nil {
		return environmentSecrets{}, false, fmt.Errorf("decrypt confirmed Restore Drill credentials: %w", err)
	}
	return secrets, true, nil
}

func completeRestoreDrillGate(backupRoot string) error {
	gatePath, credentialsPath := restoreDrillGatePaths(backupRoot)
	if err := os.Remove(gatePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(credentialsPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
