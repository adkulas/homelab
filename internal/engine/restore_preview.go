package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const restorePreviewSchemaVersion = "homelab.media-stack/restore-preview/v1alpha1"

type restorePreviewArtifact struct {
	SchemaVersion        string   `json:"schemaVersion"`
	Environment          string   `json:"environment"`
	ProjectName          string   `json:"projectName"`
	SourceManifestPath   string   `json:"sourceManifestPath"`
	SourceManifestSHA256 string   `json:"sourceManifestSHA256"`
	Preview              string   `json:"preview"`
	Services             []string `json:"services"`
	CredentialsSHA256    string   `json:"credentialsSHA256,omitempty"`
}

func prepareOrConfirmRestorePreview(backupRoot string, manifest []byte, report RestoreReport, confirm bool) (string, error) {
	artifact := restorePreviewArtifact{
		SchemaVersion:        restorePreviewSchemaVersion,
		Environment:          report.Environment,
		ProjectName:          report.ProjectName,
		SourceManifestPath:   report.BackupPath,
		SourceManifestSHA256: checksum(manifest),
		Preview:              report.Preview,
		Services:             report.Services,
		CredentialsSHA256:    report.credentialsSHA256,
	}
	contents, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode restore preview: %w", err)
	}
	contents = append(contents, '\n')
	directory := filepath.Join(backupRoot, ".restore-previews")
	path := filepath.Join(directory, checksum(contents)+".json")
	if !confirm {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", fmt.Errorf("create restore preview directory: %w", err)
		}
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, contents, 0o600); err != nil {
			return "", fmt.Errorf("write restore preview: %w", err)
		}
		if err := os.Rename(temporary, path); err != nil {
			return "", fmt.Errorf("publish restore preview: %w", err)
		}
		return path, nil
	}

	preview, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("restore confirmation requires an unchanged preview; run restore without --confirm first")
	}
	if err != nil {
		return "", fmt.Errorf("read restore preview: %w", err)
	}
	if !bytes.Equal(preview, contents) {
		return "", fmt.Errorf("restore confirmation requires an unchanged preview; run restore without --confirm first")
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("consume restore preview: %w", err)
	}
	return path, nil
}
