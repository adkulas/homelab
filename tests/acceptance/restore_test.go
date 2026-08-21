package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreRejectsChecksumFailureBeforeReplacingState(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read backup manifest: %v", err)
	}
	var manifest struct {
		Services []struct {
			ArchivePath string `json:"archivePath"`
		} `json:"services"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode backup manifest: %v", err)
	}
	archivePath := filepath.Join(filepath.Dir(manifestPath), filepath.FromSlash(manifest.Services[0].ArchivePath))
	if err := os.WriteFile(archivePath, []byte("corrupted"), 0o600); err != nil {
		t.Fatalf("corrupt backup archive: %v", err)
	}

	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly accepted a corrupt archive:\n%s", output)
	}
	if !strings.Contains(string(output), "checksum") {
		t.Fatalf("restore checksum error = %s", output)
	}
	if calls, readErr := os.ReadFile(filepath.Join(temporary, "restore-docker.log")); readErr == nil && strings.Contains(string(calls), "volume rm") {
		t.Fatalf("restore replaced state before checksum validation:\n%s", calls)
	}
}

func TestRestoreRejectsIncompleteMutableServiceCoverage(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read backup manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("decode backup manifest: %v", err)
	}
	services := manifest["services"].([]any)
	manifest["services"] = services[:len(services)-1]
	contents, err = json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode incomplete backup manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, contents, 0o600); err != nil {
		t.Fatalf("write incomplete backup manifest: %v", err)
	}

	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore accepted incomplete service coverage:\n%s", output)
	}
	if !strings.Contains(string(output), "complete mutable service coverage") {
		t.Fatalf("restore coverage error = %s", output)
	}
}

func TestRestorePreviewsReplacementBeforeCancellation(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--output", "json")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly proceeded without confirmation:\n%s", output)
	}
	for _, want := range []string{
		`"preview":"replace staging Environment state from staging backup"`,
		"restore requires --confirm",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("restore cancellation output missing %q: %s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(temporary, "restore-docker.log")); err == nil {
		t.Fatalf("cancelled restore invoked Docker")
	}
}

func TestRestoreReplacesMutableStateAndRecordsRecoveryOperation(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		if err := os.WriteFile(path, []byte("drifted\n"), 0o600); err != nil {
			t.Fatalf("mutate %s state: %v", serviceName, err)
		}
	}

	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm", "--output", "json")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack restore failed: %v\n%s", err, output)
	}
	var report struct {
		Completed            bool   `json:"completed"`
		SafetyBackupPath     string `json:"safetyBackupPath"`
		OperationJournalPath string `json:"operationJournalPath"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode restore report: %v\n%s", err, output)
	}
	if !report.Completed || report.SafetyBackupPath == "" || report.OperationJournalPath == "" {
		t.Fatalf("restore completion evidence is incomplete: %#v", report)
	}
	if contents, err := os.ReadFile(report.SafetyBackupPath); err != nil || !json.Valid(contents) {
		t.Fatalf("verified safety backup manifest is unavailable: %v", err)
	}
	journal, err := os.ReadFile(report.OperationJournalPath)
	if err != nil {
		t.Fatalf("read restore operation journal: %v", err)
	}
	if !strings.Contains(string(journal), `"status": "completed"`) {
		t.Fatalf("restore journal is not complete: %s", journal)
	}
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read recovered %s state: %v", serviceName, err)
		}
		if string(contents) != serviceName+"\n" {
			t.Fatalf("recovered %s state = %q", serviceName, contents)
		}
	}
	dockerCalls, err := os.ReadFile(filepath.Join(temporary, "restore-docker.log"))
	if err != nil {
		t.Fatalf("read restore Docker calls: %v", err)
	}
	for _, want := range []string{"compose", "stop", "volume create", "volume rm", "up -d"} {
		if !strings.Contains(string(dockerCalls), want) {
			t.Fatalf("restore Docker calls omit %q:\n%s", want, dockerCalls)
		}
	}
}

func TestRestoreRejectsEnvironmentMismatch(t *testing.T) {
	temporary := t.TempDir()
	configPath, backupPath, fixtureRoot := createRestorableBackup(t, temporary, "production")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--confirm")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "restore requires a backup from the staging Environment") {
		t.Fatalf("restore mismatch error = %s", output)
	}
}

func TestRestoreRejectsRestoreDrillWithoutCredentials(t *testing.T) {
	temporary := t.TempDir()
	configPath, backupPath, fixtureRoot := createRestorableBackup(t, temporary, "production")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--confirm", "--as-restore-drill")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore drill unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "restore drill requires --credentials") {
		t.Fatalf("restore drill credential error = %s", output)
	}
}

func TestRestoreDrillPreviewsProductionIntoStagingIsolation(t *testing.T) {
	temporary := t.TempDir()
	configPath, backupPath, fixtureRoot := createRestorableBackup(t, temporary, "production")
	credentialsPath := filepath.Join(temporary, "staging-drill.sops.yaml")
	if err := os.WriteFile(credentialsPath, []byte("credentials: rotated\n"), 0o600); err != nil {
		t.Fatalf("write drill credentials: %v", err)
	}
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--confirm", "--as-restore-drill", "--credentials", credentialsPath, "--output", "json")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
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
		`"credentialsPath":"`,
		filepath.Base(credentialsPath),
		`"preview":"restore drill: replace staging Environment state from production backup with acquisition disabled, integrations gated, and credentials overridden from staging-drill.sops.yaml"`,
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

func createRestorableBackup(t *testing.T, temporary, environment string) (configPath, manifestPath, fixtureRoot string) {
	t.Helper()
	configPath = backupConfig(t, repositoryRoot(t), temporary)
	fixtureRoot = filepath.Join(temporary, "volumes")
	projectName := "media-" + environment
	createBackupVolumeFixtures(t, fixtureRoot, projectName)
	command := backupCommand(t, "--environment", environment, "--config", configPath, "--output", "json")
	command.Env = append(os.Environ(),
		"PATH="+fakeDockerPath(t, temporary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_FIXTURE_ROOT="+fixtureRoot,
		"FAKE_DOCKER_LOG="+filepath.Join(temporary, "backup-docker.log"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("create restorable backup: %v\n%s", err, output)
	}
	var report struct {
		ManifestPath string `json:"manifestPath"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode backup report: %v\n%s", err, output)
	}
	return configPath, report.ManifestPath, fixtureRoot
}

func restoreEnvironment(t *testing.T, temporary, fixtureRoot string) []string {
	t.Helper()
	return append(os.Environ(),
		"PATH="+fakeDockerPath(t, temporary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_FIXTURE_ROOT="+fixtureRoot,
		"FAKE_DOCKER_LOG="+filepath.Join(temporary, "restore-docker.log"),
	)
}
