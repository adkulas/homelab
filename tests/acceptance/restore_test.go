package acceptance_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestRestoreDrillRestoresSafeStagingStateWithRotatedCredentials(t *testing.T) {
	temporary := t.TempDir()
	configPath, backupPath, fixtureRoot := createRestorableBackup(t, temporary, "production")
	recoveredBehaviorObserved := make(chan struct{}, 1)
	username, password := "production-household", "production-jellyfin-password"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for recovered Jellyfin API: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /System/Info/Public":
			_ = json.NewEncoder(writer).Encode(map[string]any{"StartupWizardCompleted": true})
		case "POST /Users/AuthenticateByName":
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["Username"] != username || body["Pw"] != password {
				http.Error(writer, "invalid credentials", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"AccessToken": "restore-drill-token",
				"User":        map[string]any{"Id": "recovered-user", "Name": username, "Policy": map[string]any{"IsAdministrator": true}},
			})
		case "GET /Users/recovered-user":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"Id": "recovered-user", "Name": username, "Policy": map[string]any{"IsAdministrator": true}, "Configuration": map[string]any{},
			})
		case "POST /Users/Password":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["CurrentPw"] != "production-jellyfin-password" || body["NewPw"] != "drill-jellyfin-password" {
				http.Error(writer, "wrong password rotation", http.StatusBadRequest)
				return
			}
			password = "drill-jellyfin-password"
			writer.WriteHeader(http.StatusNoContent)
		case "POST /Users":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["Name"] != "drill-household" {
				http.Error(writer, "wrong administrator rotation", http.StatusBadRequest)
				return
			}
			username = "drill-household"
			writer.WriteHeader(http.StatusNoContent)
		case "GET /Library/VirtualFolders":
			_ = json.NewEncoder(writer).Encode([]any{
				map[string]any{"Name": "Movie Library", "CollectionType": "movies", "Locations": []string{"/data/media/movies"}},
				map[string]any{"Name": "Series Library", "CollectionType": "tvshows", "Locations": []string{"/data/media/series"}},
			})
			select {
			case recoveredBehaviorObserved <- struct{}{}:
			default:
			}
		default:
			http.NotFound(writer, request)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	configContents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read drill configuration: %v", err)
	}
	jellyfinPort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	configContents = []byte(strings.Replace(string(configContents), "jellyfin: 18096", "jellyfin: "+jellyfinPort, 1))
	if err := os.WriteFile(configPath, configContents, 0o600); err != nil {
		t.Fatalf("write drill Jellyfin port: %v", err)
	}
	createBackupVolumeFixtures(t, fixtureRoot, "media-staging")
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		if err := os.WriteFile(path, []byte("staging-"+serviceName+"\n"), 0o600); err != nil {
			t.Fatalf("write pre-drill %s state: %v", serviceName, err)
		}
	}
	credentialsPath, runtimeDirectory, drillEnvironment := prepareRestoreDrill(t, temporary, fixtureRoot, configPath, backupPath)
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--confirm", "--as-restore-drill", "--credentials", credentialsPath, "--output", "json")
	command.Env = drillEnvironment
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
		`"excludedServices":["profilarr"]`,
		`"startedServices":["jellyfin"]`,
		`"completed":true`,
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("restore drill report missing %s: %s", want, output)
		}
	}
	for _, sensitive := range []string{credentialsPath, filepath.Base(credentialsPath), "drill-user", "drill-password"} {
		if strings.Contains(string(output), sensitive) {
			t.Fatalf("restore drill report exposed sensitive credential reference %q: %s", sensitive, output)
		}
	}
	for _, serviceName := range backupFixtureServiceNames {
		path := filepath.Join(fixtureRoot, "media-staging_"+serviceName+"-config", "identity.txt")
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read post-drill %s state: %v", serviceName, err)
		}
		want := serviceName + "\n"
		if serviceName == "profilarr" {
			want = "staging-profilarr\n"
		}
		if string(contents) != want {
			t.Fatalf("post-drill %s state = %q, want %q", serviceName, contents, want)
		}
	}
	secretRoot := filepath.Join(runtimeDirectory, "media-stack", "media-staging")
	for path, want := range map[string]string{
		filepath.Join(secretRoot, "openvpn_user"):     "drill-user\n",
		filepath.Join(secretRoot, "openvpn_password"): "drill-password\n",
		filepath.Join(secretRoot, "profilarr.env"):    "PROFILARR_API_KEY=drill-profilarr-api-key-32-characters\n",
	} {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != want {
			t.Fatalf("materialized drill credential %s = %q (%v), want %q", filepath.Base(path), contents, err, want)
		}
	}
	dockerCalls, err := os.ReadFile(filepath.Join(temporary, "restore-docker.log"))
	if err != nil {
		t.Fatalf("read restore Docker calls: %v", err)
	}
	if !strings.Contains(string(dockerCalls), " up -d --no-deps jellyfin") {
		t.Fatalf("restore drill did not start only recovered Jellyfin:\n%s", dockerCalls)
	}
	select {
	case <-recoveredBehaviorObserved:
	default:
		t.Fatal("restore drill did not rotate credentials and verify recovered Jellyfin libraries through its supported API")
	}
	blockedApply := applyCommand(t, "--environment", "staging", "--config", configPath)
	blockedApply.Env = drillEnvironment
	blockedOutput, blockedErr := blockedApply.CombinedOutput()
	if blockedErr == nil {
		t.Fatalf("apply bypassed the Restore Drill integration gate:\n%s", blockedOutput)
	}
	if !strings.Contains(string(blockedOutput), "Restore Drill integrations require explicit confirmation") {
		t.Fatalf("apply integration-gate error = %s", blockedOutput)
	}
	confirmIntegrations := restoreCommand(t,
		"--environment", "staging",
		"--config", configPath,
		"--as-restore-drill",
		"--credentials", credentialsPath,
		"--confirm-integrations",
		"--output", "json",
	)
	confirmIntegrations.Env = drillEnvironment
	confirmedOutput, confirmedErr := confirmIntegrations.CombinedOutput()
	if confirmedErr != nil {
		t.Fatalf("confirm Restore Drill integrations: %v\n%s", confirmedErr, confirmedOutput)
	}
	if !strings.Contains(string(confirmedOutput), `"integrationsGated":false`) {
		t.Fatalf("integration confirmation report = %s", confirmedOutput)
	}
}

func TestRestoreDrillRollsBackStagingStateAndCredentialsWhenSafeStartupFails(t *testing.T) {
	temporary := t.TempDir()
	configPath, backupPath, fixtureRoot := createRestorableBackup(t, temporary, "production")
	createBackupVolumeFixtures(t, fixtureRoot, "media-staging")
	writeRestoreFixtureState(t, fixtureRoot, "before-drill-")
	credentialsPath, runtimeDirectory, drillEnvironment := prepareRestoreDrill(t, temporary, fixtureRoot, configPath, backupPath)

	marker := filepath.Join(temporary, "drill-start-failed-once")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--confirm", "--as-restore-drill", "--credentials", credentialsPath)
	command.Env = append(drillEnvironment, "FAKE_DOCKER_FAIL_COMPOSE_UP_ONCE_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore drill unexpectedly hid safe startup failure:\n%s", output)
	}
	if !strings.Contains(string(output), "start recovered Jellyfin with acquisition and integrations gated") {
		t.Fatalf("restore drill startup error = %s", output)
	}
	assertRestoreFixtureState(t, fixtureRoot, "before-drill-")
	assertLatestRestoreJournalStatus(t, temporary, "rolled-back")

	secretRoot := filepath.Join(runtimeDirectory, "media-stack", "media-staging")
	for path, want := range map[string]string{
		filepath.Join(secretRoot, "openvpn_user"):     "staging-user\n",
		filepath.Join(secretRoot, "openvpn_password"): "staging-password\n",
		filepath.Join(secretRoot, "profilarr.env"):    "PROFILARR_API_KEY=staging-profilarr-api-key-32-characters\n",
	} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil || string(contents) != want {
			t.Fatalf("restored Staging credential %s = %q (%v), want %q", filepath.Base(path), contents, readErr, want)
		}
	}
}

func prepareRestoreDrill(t *testing.T, temporary, fixtureRoot, configPath, backupPath string) (string, string, []string) {
	t.Helper()
	credentialsPath := filepath.Join(temporary, "staging-drill.sops.yaml")
	writeFile(t, credentialsPath, []byte("encrypted: true\n"), 0o600)
	binDirectory := fakeDockerPath(t, temporary)
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
case "${4##*/}" in
	staging-drill.sops.yaml) prefix=drill ;;
	production.sops.yaml) prefix=production ;;
	*) prefix=staging ;;
esac
printf 'nordvpn:\n  openvpn:\n    serviceUsername: %s-user\n    servicePassword: %s-password\nprofilarr:\n  apiKey: %s-profilarr-api-key-32-characters\njellyfin:\n  username: %s-household\n  password: %s-jellyfin-password\nqbittorrent:\n  username: %s-household\n  password: %s-qbittorrent-password\n' "$prefix" "$prefix" "$prefix" "$prefix" "$prefix" "$prefix" "$prefix"
`), 0o700)
	runtimeDirectory := filepath.Join(temporary, "runtime")
	drillEnvironment := append(restoreEnvironment(t, temporary, fixtureRoot),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+runtimeDirectory,
	)
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", backupPath, "--as-restore-drill", "--credentials", credentialsPath)
	preview.Env = drillEnvironment
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack Restore Drill did not produce the required preview:\n%s", output)
	}
	return credentialsPath, runtimeDirectory, drillEnvironment
}

func applyCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	goArguments := append([]string{"run", "../../cmd/media-stack", "apply"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
	return command
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
func TestRestoreRollsBackPartiallyFailedComposeShutdown(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	writeRestoreFixtureState(t, fixtureRoot, "pre-shutdown-failure-")
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath)
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore did not produce shutdown rollback preview:\n%s", output)
	}

	marker := filepath.Join(temporary, "compose-down-failed-once")
	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = append(restoreEnvironment(t, temporary, fixtureRoot),
		"FAKE_DOCKER_FAIL_COMPOSE_DOWN_ONCE_MARKER="+marker,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly hid shutdown failure:\n%s", output)
	}
	if !strings.Contains(string(output), "remove service containers for restore") {
		t.Fatalf("restore shutdown error = %s", output)
	}
	assertLatestRestoreJournalStatus(t, temporary, "rolled-back")
	assertRestoreFixtureState(t, fixtureRoot, "pre-shutdown-failure-")
}

func TestRestoreDoesNotStartServicesAfterIncompleteRollback(t *testing.T) {
	temporary := t.TempDir()
	configPath, manifestPath, fixtureRoot := createRestorableBackup(t, temporary, "staging")
	writeRestoreFixtureState(t, fixtureRoot, "pre-incomplete-rollback-")
	preview := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath)
	preview.Env = restoreEnvironment(t, temporary, fixtureRoot)
	if output, err := preview.CombinedOutput(); err == nil || !strings.Contains(string(output), "restore requires --confirm") {
		t.Fatalf("media-stack restore did not produce incomplete rollback preview:\n%s", output)
	}

	command := restoreCommand(t, "--environment", "staging", "--config", configPath, "--backup", manifestPath, "--confirm")
	command.Env = append(restoreEnvironment(t, temporary, fixtureRoot),
		"FAKE_DOCKER_FAIL_RESTORE_VOLUME_ALWAYS=media-staging_radarr-config",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack restore unexpectedly hid incomplete rollback:\n%s", output)
	}
	assertLatestRestoreJournalStatus(t, temporary, "rollback-failed")
	dockerCalls, err := os.ReadFile(filepath.Join(temporary, "restore-docker.log"))
	if err != nil {
		t.Fatalf("read restore Docker calls: %v", err)
	}
	if strings.Contains(string(dockerCalls), " up -d") {
		t.Fatalf("restore exposed incomplete rollback state by starting services:\n%s", dockerCalls)
	}
}
