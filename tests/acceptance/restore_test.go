package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreRejectsCancellationWithoutConfirmation(t *testing.T) {
	backupPath := writeBackupManifest(t, "staging")
	command := restoreCommand(t, "--environment", "staging", "--backup", backupPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("restore cancellation error = %s", output)
	}
}

func TestRestoreRejectsEnvironmentMismatch(t *testing.T) {
	backupPath := writeBackupManifest(t, "production")
	command := restoreCommand(t, "--environment", "staging", "--backup", backupPath, "--confirm")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "restore requires a backup from the staging Environment") {
		t.Fatalf("restore mismatch error = %s", output)
	}
}

func TestRestorePreviewsMatchingEnvironmentRestoration(t *testing.T) {
	backupPath := writeBackupManifest(t, "staging")
	command := restoreCommand(t, "--environment", "staging", "--backup", backupPath, "--confirm", "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack restore failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"environment":"staging"`) {
		t.Fatalf("restore report missing environment: %s", output)
	}
	if !strings.Contains(string(output), `"preview":"replace staging Environment state from staging backup"`) {
		t.Fatalf("restore report missing preview: %s", output)
	}
}

func TestRestoreDrillPreviewsProductionIntoStagingIsolation(t *testing.T) {
	backupPath := writeBackupManifest(t, "production")
	command := restoreCommand(t, "--environment", "staging", "--backup", backupPath, "--confirm", "--as-restore-drill", "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack restore drill failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		`"environment":"staging"`,
		`"restoreDrill":true`,
		`"sourceEnvironment":"production"`,
		`"acquisitionDisabled":true`,
		`"integrationsGated":true`,
		`"preview":"restore drill: replace staging Environment state from production backup with acquisition disabled and integrations gated"`,
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("restore drill report missing %s: %s", want, output)
		}
	}
}

func restoreCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	goArguments := append([]string{"run", "../../cmd/media-stack", "restore"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
	return command
}

func writeBackupManifest(t *testing.T, environment string) string {
	t.Helper()
	command := backupCommand(t, "--environment", environment, "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare backup manifest: %v\n%s", err, output)
	}
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(path, output, 0o600); err != nil {
		t.Fatalf("write backup manifest: %v", err)
	}
	return path
}
