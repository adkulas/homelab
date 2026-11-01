package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adkulas/homelab/internal/config"
)

func selectedSecretPath(configPath string, environment config.Environment) string {
	path := environment.SecretsFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(configPath), path)
	}
	return path
}

// patchEncryptedSecrets edits an encrypted temporary copy. Secret values travel
// only over stdin; plaintext is never written to disk or included in diagnostics.
func patchEncryptedSecrets(ctx context.Context, path string, branches map[string]any) error {
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read encrypted companion secrets: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".media-stack-companions-*.sops.yaml")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	defer os.Remove(tempPath)
	if _, err = temporary.Write(original); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	for branch, value := range branches {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode companion secrets")
		}
		index, _ := json.Marshal([]string{branch})
		command := exec.CommandContext(ctx, "sops", "set", "--value-stdin", "--input-type", "yaml", "--output-type", "yaml", tempPath, string(index))
		command.Stdin = bytes.NewReader(encoded)
		if err := command.Run(); err != nil {
			return fmt.Errorf("encrypt companion secrets with SOPS; original document preserved")
		}
	}
	if _, err := decryptEnvironmentSecrets(ctx, tempPath); err != nil {
		return fmt.Errorf("validate updated companion SOPS document; original document preserved")
	}
	updated, err := os.ReadFile(tempPath)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(original, current) {
		return fmt.Errorf("companion secrets changed concurrently; retry with the current SOPS document")
	}
	return writeSecretAtomic(path, updated)
}
