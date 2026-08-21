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

func TestRestoreRejectsConfirmationWithoutAnUnchangedPreview(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = restoreEnvironment(t, temporary, fixtureRoot)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore bypassed preview-before-confirmation:\n%s", output)
	}
	if !strings.Contains(string(output), "run restore without --confirm first") {
		t.Fatalf("restore preview prerequisite error = %s", output)
	}
	if _, err := os.Stat(filepath.Join(temporary, "restore-docker.log")); err == nil {
		t.Fatalf("unpreviewed restore invoked Docker")
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

	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--output", "json")
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore did not produce the required preview:\n%s", output)
	}
	if err := os.Remove(filepath.Join(temporary, "restore-docker.log")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("reset restore Docker log: %v", err)
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
	for _, want := range []string{"compose", "down", "volume create", "volume rm", "cp --archive", "up -d"} {
		if !strings.Contains(string(dockerCalls), want) {
			t.Fatalf("restore Docker calls omit %q:\n%s", want, dockerCalls)
		}
	}
}

func TestRestoreRollsBackPartialReplacementFromSafetyBackup(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		if err := os.WriteFile(path, []byte("pre-restore-"+serviceName+"\n"), 0o600); err != nil {
			t.Fatalf("write pre-restore %s state: %v", serviceName, err)
		}
	}
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath)
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore did not produce rollback test preview:\n%s", output)
	}

	marker := filepath.Join(temporary, "restore-failed-once")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = append(restoreEnvironment(t, temporary, fixtureRoot),
		"FAKE_DOCKER_FAIL_RESTORE_VOLUME=media-staging_radarr-config",
		"FAKE_DOCKER_FAIL_ONCE_MARKER="+marker,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly hid replacement failure:\n%s", output)
	}
	if !strings.Contains(string(output), "replace radarr mutable volume") {
		t.Fatalf("restore replacement error = %s", output)
	}
	journals, err := filepath.Glob(filepath.Join(temporary, "backups", "staging", ".restore-operations", "*.json"))
	if err != nil || len(journals) != 1 {
		t.Fatalf("restore journals = %#v (%v)", journals, err)
	}
	journal, err := os.ReadFile(journals[0])
	if err != nil {
		t.Fatalf("read rollback journal: %v", err)
	}
	if !strings.Contains(string(journal), `"status": "rolled-back"`) {
		t.Fatalf("restore did not record successful rollback: %s", journal)
	}
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read rolled-back %s state: %v", serviceName, err)
		}
		if string(contents) != "pre-restore-"+serviceName+"\n" {
			t.Fatalf("rolled-back %s state = %q", serviceName, contents)
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
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--as-restore-drill", "--credentials", credentialsPath)
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore drill did not produce the required preview:\n%s", output)
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
		"FAKE_DOCKER_COMPOSE_DOWN_MARKER="+filepath.Join(temporary, "compose-down"),
	)
}
func TestRestoreRollsBackWhenDependencyStartupPartiallyFails(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	writeRestoreFixtureState(t, fixtureRoot, "pre-startup-failure-")
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath)
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore did not produce startup rollback preview:\n%s", output)
	}

	marker := filepath.Join(temporary, "compose-up-failed-once")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = append(restoreEnvironment(t, temporary, fixtureRoot),
		"FAKE_DOCKER_FAIL_COMPOSE_UP_ONCE_MARKER="+marker,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly hid startup failure:\n%s", output)
	}
	if !strings.Contains(string(output), "start restored services in dependency order") {
		t.Fatalf("restore startup error = %s", output)
	}
	assertLatestRestoreJournalStatus(t, temporary, "rolled-back")
	assertRestoreFixtureState(t, fixtureRoot, "pre-startup-failure-")

	dockerCalls, err := os.ReadFile(filepath.Join(temporary, "restore-docker.log"))
	if err != nil {
		t.Fatalf("read restore Docker calls: %v", err)
	}
	if strings.Count(string(dockerCalls), " down") < 2 || strings.Count(string(dockerCalls), " up -d") < 2 {
		t.Fatalf("rollback did not remove partial startup containers before replacing volumes:\n%s", dockerCalls)
	}
}

func TestRestoreRecoversKilledReplacementFromOperationJournal(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	writeRestoreFixtureState(t, fixtureRoot, "pre-interruption-")
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath)
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore did not produce interruption preview:\n%s", output)
	}

	marker := filepath.Join(temporary, "restore-killed-once")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = append(restoreEnvironment(t, temporary, fixtureRoot),
		"FAKE_DOCKER_KILL_ON_RESTORE_VOLUME=media-staging_radarr-config",
		"FAKE_DOCKER_KILL_ONCE_MARKER="+marker,
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("media-stack restore unexpectedly survived injected process death:\n%s", output)
	}

	recoverCommand := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath)
	recoverCommand.Env = restoreEnvironment(t, temporary, fixtureRoot)
	recoveryOutput, err := recoverCommand.CombinedOutput()
	if err == nil || !strings.Contains(string(recoveryOutput), "restore requires --confirm") {
		t.Fatalf("next restore did not recover and return a fresh preview: %v\n%s", err, recoveryOutput)
	}
	assertLatestRestoreJournalStatus(t, temporary, "rolled-back")
	assertRestoreFixtureState(t, fixtureRoot, "pre-interruption-")

	dockerCalls, err := os.ReadFile(filepath.Join(temporary, "restore-docker.log"))
	if err != nil {
		t.Fatalf("read restore Docker calls: %v", err)
	}
	if !strings.Contains(string(dockerCalls), "ps --all --quiet --filter volume=") {
		t.Fatalf("interruption recovery did not remove orphaned helper containers:\n%s", dockerCalls)
	}
}

func writeRestoreFixtureState(t *testing.T, fixtureRoot, prefix string) {
	t.Helper()
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		if err := os.WriteFile(path, []byte(prefix+serviceName+"\n"), 0o600); err != nil {
			t.Fatalf("write pre-restore %s state: %v", serviceName, err)
		}
	}
}

func assertRestoreFixtureState(t *testing.T, fixtureRoot, prefix string) {
	t.Helper()
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read recovered %s state: %v", serviceName, err)
		}
		if string(contents) != prefix+serviceName+"\n" {
			t.Fatalf("recovered %s state = %q", serviceName, contents)
		}
	}
}

func assertLatestRestoreJournalStatus(t *testing.T, temporary, status string) {
	t.Helper()
	journals, err := filepath.Glob(filepath.Join(temporary, "backups", "staging", ".restore-operations", "*.json"))
	if err != nil || len(journals) != 1 {
		t.Fatalf("restore journals = %#v (%v)", journals, err)
	}
	journal, err := os.ReadFile(journals[0])
	if err != nil {
		t.Fatalf("read restore journal: %v", err)
	}
	if !strings.Contains(string(journal), `"status": "`+status+`"`) {
		t.Fatalf("restore journal status is not %s: %s", status, journal)
	}
}
