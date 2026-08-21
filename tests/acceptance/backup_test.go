package acceptance_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var backupFixtureServiceNames = []string{"gluetun", "qbittorrent", "prowlarr", "sonarr", "radarr", "profilarr", "jellyfin", "seerr"}

func TestBackupAppliesConfiguredRetentionToPublishedEnvironmentArchives(t *testing.T) {
	repository := repositoryRoot(t)
	temporary := t.TempDir()
	backupRoot := filepath.Join(temporary, "backups", "staging")
	configPath := backupConfig(t, repository, temporary)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Declared Configuration: %v", err)
	}
	contents = bytes.Replace(contents, []byte("        backupRetention:\n            daily: 7\n            weekly: 4\n            monthly: 6\n"), []byte("        backupRetention:\n            daily: 1\n            weekly: 1\n            monthly: 1\n"), 1)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatalf("configure backup retention: %v", err)
	}

	fixtures := []struct {
		id          string
		generatedAt string
		protected   bool
	}{
		{id: "daily-expired-same-bucket", generatedAt: "2026-08-21T08:00:00Z"},
		{id: "weekly-survivor", generatedAt: "2026-08-16T12:00:00Z"},
		{id: "weekly-expired-same-bucket", generatedAt: "2026-08-15T12:00:00Z"},
		{id: "monthly-survivor", generatedAt: "2026-07-15T12:00:00Z"},
		{id: "monthly-expired-same-bucket", generatedAt: "2026-07-01T12:00:00Z"},
		{id: "expired", generatedAt: "2026-06-30T12:00:00Z"},
		{id: "protected", generatedAt: "2024-01-01T12:00:00Z", protected: true},
	}
	for _, fixture := range fixtures {
		writeBackupRetentionFixture(t, backupRoot, fixture.id, fixture.generatedAt, fixture.protected)
	}

	createdID := runBackupForRetention(t, temporary, configPath)
	got := retainedBackupDirectories(t, backupRoot)
	want := []string{createdID, "monthly-survivor", "protected", "weekly-survivor"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained backup directories = %#v, want %#v", got, want)
	}
}

func TestBackupAppliesDefaultSevenDailyFourWeeklySixMonthlyRetention(t *testing.T) {
	repository := repositoryRoot(t)
	temporary := t.TempDir()
	backupRoot := filepath.Join(temporary, "backups", "staging")
	configPath := backupConfig(t, repository, temporary)
	fixtures := []struct {
		id          string
		generatedAt string
		protected   bool
	}{
		{id: "daily-1", generatedAt: "2026-08-20T12:00:00Z"},
		{id: "daily-2", generatedAt: "2026-08-19T12:00:00Z"},
		{id: "daily-3", generatedAt: "2026-08-18T12:00:00Z"},
		{id: "daily-4", generatedAt: "2026-08-17T12:00:00Z"},
		{id: "daily-5", generatedAt: "2026-08-16T12:00:00Z"},
		{id: "daily-6", generatedAt: "2026-08-15T12:00:00Z"},
		{id: "weekly-1", generatedAt: "2026-08-09T12:00:00Z"},
		{id: "weekly-2", generatedAt: "2026-08-02T12:00:00Z"},
		{id: "weekly-3", generatedAt: "2026-07-26T12:00:00Z"},
		{id: "weekly-4", generatedAt: "2026-07-19T12:00:00Z"},
		{id: "monthly-1", generatedAt: "2026-06-15T12:00:00Z"},
		{id: "monthly-2", generatedAt: "2026-05-15T12:00:00Z"},
		{id: "monthly-3", generatedAt: "2026-04-15T12:00:00Z"},
		{id: "monthly-4", generatedAt: "2026-03-15T12:00:00Z"},
		{id: "monthly-5", generatedAt: "2026-02-15T12:00:00Z"},
		{id: "monthly-6", generatedAt: "2026-01-15T12:00:00Z"},
		{id: "expired", generatedAt: "2025-12-15T12:00:00Z"},
		{id: "protected", generatedAt: "2020-01-01T12:00:00Z", protected: true},
	}
	for _, fixture := range fixtures {
		writeBackupRetentionFixture(t, backupRoot, fixture.id, fixture.generatedAt, fixture.protected)
	}

	createdID := runBackupForRetention(t, temporary, configPath)
	got := retainedBackupDirectories(t, backupRoot)
	want := []string{
		createdID,
		"daily-1", "daily-2", "daily-3", "daily-4", "daily-5", "daily-6",
		"weekly-1", "weekly-2", "weekly-3", "weekly-4",
		"monthly-1", "monthly-2", "monthly-3", "monthly-4", "monthly-5", "monthly-6",
		"protected",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained backup directories = %#v, want %#v", got, want)
	}
}

func runBackupForRetention(t *testing.T, temporary, configPath string) string {
	t.Helper()
	fixtureRoot := filepath.Join(temporary, "volumes")
	createBackupVolumeFixtures(t, fixtureRoot, "media-staging")
	command := backupCommand(t, "--environment", "staging", "--config", configPath, "--output", "json", "--now", "2026-08-21T12:00:00Z")
	command.Env = append(os.Environ(),
		"PATH="+fakeDockerPath(t, temporary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_FIXTURE_ROOT="+fixtureRoot,
		"FAKE_DOCKER_LOG="+filepath.Join(temporary, "docker.log"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack backup failed: %v\n%s", err, output)
	}
	var report struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode backup report: %v\n%s", err, output)
	}
	return report.ID
}

func retainedBackupDirectories(t *testing.T, backupRoot string) []string {
	t.Helper()
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		t.Fatalf("read retained backups: %v", err)
	}
	var retained []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".incomplete-") {
			retained = append(retained, entry.Name())
		}
	}
	sort.Strings(retained)
	return retained
}

func writeBackupRetentionFixture(t *testing.T, backupRoot, id, generatedAt string, protected bool) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339, generatedAt); err != nil {
		t.Fatalf("invalid fixture time %q: %v", generatedAt, err)
	}
	directory := filepath.Join(backupRoot, id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create backup retention fixture: %v", err)
	}
	manifest := map[string]any{
		"schemaVersion": "homelab.media-stack/backup/v1alpha1",
		"id":            id,
		"environment":   "staging",
		"generatedAt":   generatedAt,
		"protected":     protected,
		"complete":      true,
		"services":      []any{},
	}
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode backup retention fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), append(contents, byte(10)), 0o600); err != nil {
		t.Fatalf("write backup retention fixture: %v", err)
	}
}

func TestBackupPublishesVerifiedArchivesForEveryMutableServiceVolume(t *testing.T) {
	repository := repositoryRoot(t)
	temporary := t.TempDir()
	backupRoot := filepath.Join(temporary, "backups", "staging")
	configPath := backupConfig(t, repository, temporary)
	fixtureRoot := filepath.Join(temporary, "volumes")
	serviceNames := backupFixtureServiceNames
	createBackupVolumeFixtures(t, fixtureRoot, "media-staging")

	dockerLog := filepath.Join(temporary, "docker.log")
	command := backupCommand(t, "--environment", "staging", "--config", configPath, "--output", "json", "--label", "before-upgrade", "--protect")
	command.Env = append(os.Environ(),
		"PATH="+fakeDockerPath(t, temporary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_FIXTURE_ROOT="+fixtureRoot,
		"FAKE_DOCKER_LOG="+dockerLog,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack backup failed: %v\n%s", err, output)
	}

	var report struct {
		SchemaVersion      string `json:"schemaVersion"`
		CLIContractVersion string `json:"cliContractVersion"`
		ID                 string `json:"id"`
		ManifestPath       string `json:"manifestPath"`
		Environment        string `json:"environment"`
		ProjectName        string `json:"projectName"`
		Protected          bool   `json:"protected"`
		Label              string `json:"label"`
		ConfigDigest       string `json:"configDigest"`
		VersionDigest      string `json:"versionDigest"`
		Complete           bool   `json:"complete"`
		Services           []struct {
			Name              string `json:"name"`
			Volume            string `json:"volume"`
			DockerVolume      string `json:"dockerVolume"`
			MountPath         string `json:"mountPath"`
			Image             string `json:"image"`
			ArchivePath       string `json:"archivePath"`
			ChecksumSHA256    string `json:"checksumSHA256"`
			ConsistencyMethod string `json:"consistencyMethod"`
			SizeBytes         int64  `json:"sizeBytes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode backup report: %v\n%s", err, output)
	}
	if report.SchemaVersion == "" || report.CLIContractVersion == "" || report.ID == "" {
		t.Fatalf("backup identity is incomplete: %#v", report)
	}
	if report.Environment != "staging" || report.ProjectName != "media-staging" || !report.Complete {
		t.Fatalf("unexpected environment identity or completion state: %#v", report)
	}
	if !report.Protected || report.Label != "before-upgrade" {
		t.Fatalf("unexpected backup flags: %#v", report)
	}
	if len(report.ConfigDigest) != 64 || len(report.VersionDigest) != 64 {
		t.Fatalf("backup input digests are incomplete: %#v", report)
	}
	wantManifestPath := filepath.Join(backupRoot, report.ID, "manifest.json")
	if report.ManifestPath != wantManifestPath {
		t.Fatalf("manifest path = %q, want %q", report.ManifestPath, wantManifestPath)
	}
	if published, err := os.ReadFile(report.ManifestPath); err != nil || !json.Valid(published) {
		t.Fatalf("published manifest is missing or invalid: %v", err)
	}
	if len(report.Services) != len(serviceNames) {
		t.Fatalf("services = %d, want %d", len(report.Services), len(serviceNames))
	}

	seen := make(map[string]bool, len(serviceNames))
	for _, service := range report.Services {
		seen[service.Name] = true
		if service.Volume != service.Name+"-config" || service.DockerVolume != "media-staging_"+service.Volume {
			t.Fatalf("unexpected volume identity for %s: %#v", service.Name, service)
		}
		if service.MountPath == "" || !strings.Contains(service.Image, "@sha256:") {
			t.Fatalf("missing mount or immutable image metadata for %s: %#v", service.Name, service)
		}
		if service.ConsistencyMethod != "compose-stop+read-only-volume-archive" {
			t.Fatalf("consistency method for %s = %q", service.Name, service.ConsistencyMethod)
		}
		archivePath := filepath.Join(backupRoot, report.ID, filepath.FromSlash(service.ArchivePath))
		archive, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatalf("read %s archive: %v", service.Name, err)
		}
		if service.SizeBytes != int64(len(archive)) {
			t.Fatalf("%s size = %d, want %d", service.Name, service.SizeBytes, len(archive))
		}
		digest := sha256.Sum256(archive)
		if got := hex.EncodeToString(digest[:]); got != service.ChecksumSHA256 {
			t.Fatalf("%s checksum = %q, independently calculated %q", service.Name, service.ChecksumSHA256, got)
		}
		if got := archiveFile(t, archive, "identity.txt"); got != service.Name+"\n" {
			t.Fatalf("%s restore fixture = %q", service.Name, got)
		}
	}
	for _, serviceName := range serviceNames {
		if !seen[serviceName] {
			t.Fatalf("manifest omitted %s", serviceName)
		}
	}

	createBackupVolumeFixtures(t, fixtureRoot, "media-production")
	productionCommand := backupCommand(t, "--environment", "production", "--config", configPath, "--output", "json")
	productionCommand.Env = append(os.Environ(),
		"PATH="+fakeDockerPath(t, temporary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_FIXTURE_ROOT="+fixtureRoot,
		"FAKE_DOCKER_LOG="+dockerLog,
	)
	productionOutput, err := productionCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("production media-stack backup failed: %v\n%s", err, productionOutput)
	}
	var productionReport struct {
		ID           string `json:"id"`
		ManifestPath string `json:"manifestPath"`
		Environment  string `json:"environment"`
		ProjectName  string `json:"projectName"`
		Services     []struct {
			Name         string `json:"name"`
			DockerVolume string `json:"dockerVolume"`
			ArchivePath  string `json:"archivePath"`
		} `json:"services"`
	}
	if err := json.Unmarshal(productionOutput, &productionReport); err != nil {
		t.Fatalf("decode production backup report: %v\n%s", err, productionOutput)
	}
	productionRoot := filepath.Join(temporary, "backups", "production")
	if productionReport.Environment != "production" || productionReport.ProjectName != "media-production" {
		t.Fatalf("unexpected Production identity: %#v", productionReport)
	}
	if productionReport.ManifestPath != filepath.Join(productionRoot, productionReport.ID, "manifest.json") {
		t.Fatalf("Production manifest path = %q", productionReport.ManifestPath)
	}
	if productionReport.ManifestPath == report.ManifestPath {
		t.Fatalf("Production and Staging share manifest path %q", productionReport.ManifestPath)
	}
	if len(productionReport.Services) != len(backupFixtureServiceNames) {
		t.Fatalf("Production services = %d, want %d", len(productionReport.Services), len(backupFixtureServiceNames))
	}
	for _, service := range productionReport.Services {
		if service.DockerVolume != "media-production_"+service.Name+"-config" {
			t.Fatalf("Production Docker volume for %s = %q", service.Name, service.DockerVolume)
		}
		archive, err := os.ReadFile(filepath.Join(productionRoot, productionReport.ID, filepath.FromSlash(service.ArchivePath)))
		if err != nil {
			t.Fatalf("read Production %s archive: %v", service.Name, err)
		}
		if got := archiveFile(t, archive, "identity.txt"); got != service.Name+"\n" {
			t.Fatalf("Production %s restore fixture = %q", service.Name, got)
		}
	}

	dockerCalls, err := os.ReadFile(dockerLog)
	if err != nil {
		t.Fatalf("read Docker calls: %v", err)
	}
	for _, operation := range []string{"compose", "stop", "start", "create", "cp", "rm"} {
		if !bytes.Contains(dockerCalls, []byte(operation)) {
			t.Fatalf("Docker calls do not include %q:\n%s", operation, dockerCalls)
		}
	}
	incomplete, err := filepath.Glob(filepath.Join(backupRoot, ".incomplete-*"))
	if err != nil || len(incomplete) != 0 {
		t.Fatalf("successful backup left incomplete artifacts: %#v (%v)", incomplete, err)
	}
}

func TestBackupFailureResumesServicesWithoutPublishingManifest(t *testing.T) {
	repository := repositoryRoot(t)
	temporary := t.TempDir()
	configPath := backupConfig(t, repository, temporary)
	fixtureRoot := filepath.Join(temporary, "volumes")
	createBackupVolumeFixtures(t, fixtureRoot, "media-staging")

	dockerLog := filepath.Join(temporary, "docker.log")
	command := backupCommand(t, "--environment", "staging", "--config", configPath, "--output", "json")
	command.Env = append(os.Environ(),
		"PATH="+fakeDockerPath(t, temporary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_FIXTURE_ROOT="+fixtureRoot,
		"FAKE_DOCKER_LOG="+dockerLog,
		"FAKE_DOCKER_FAIL_CP=media-staging_sonarr-config",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack backup unexpectedly succeeded:\n%s", output)
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("backup failure exit = %v, want operational exit 1\n%s", err, output)
	}
	if bytes.Contains(output, []byte("exit status 64")) {
		t.Fatalf("backup data-path failure was classified as usage: %s", output)
	}

	dockerCalls, err := os.ReadFile(dockerLog)
	if err != nil {
		t.Fatalf("read Docker calls: %v", err)
	}
	if !bytes.Contains(dockerCalls, []byte(" start ")) {
		t.Fatalf("backup did not resume quiesced services after failure:\n%s", dockerCalls)
	}
	incomplete, err := filepath.Glob(filepath.Join(temporary, "backups", "staging", ".incomplete-*"))
	if err != nil || len(incomplete) != 1 {
		t.Fatalf("incomplete backups = %#v, want one (%v)", incomplete, err)
	}
	if _, err := os.Stat(filepath.Join(incomplete[0], "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("failed backup published a manifest: %v", err)
	}
}

func TestBackupRejectsOverlappingEnvironmentNamespaces(t *testing.T) {
	temporary := t.TempDir()
	configPath := backupConfig(t, repositoryRoot(t), temporary)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read backup fixture configuration: %v", err)
	}
	productionRoot := filepath.Join(temporary, "backups", "production")
	stagingRoot := filepath.Join(temporary, "backups", "staging")
	contents = bytes.ReplaceAll(contents, []byte(stagingRoot), []byte(productionRoot))
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatalf("write overlapping backup roots: %v", err)
	}

	output, err := backupCommand(t, "--environment", "staging", "--config", configPath).CombinedOutput()
	if err == nil {
		t.Fatalf("backup with overlapping Environment namespaces unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "Production and Staging backup roots must not overlap") {
		t.Fatalf("backup namespace error = %s", output)
	}
}

func TestBackupRejectsNamespaceInsideOtherEnvironmentData(t *testing.T) {
	temporary := t.TempDir()
	configPath := backupConfig(t, repositoryRoot(t), temporary)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read backup fixture configuration: %v", err)
	}
	productionRoot := filepath.Join(temporary, "backups", "production")
	contents = bytes.ReplaceAll(contents, []byte(productionRoot), []byte("/srv/media/staging/backups"))
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatalf("write cross-Environment backup root: %v", err)
	}

	output, err := backupCommand(t, "--environment", "production", "--config", configPath).CombinedOutput()
	if err == nil {
		t.Fatalf("backup inside Staging data unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "backup roots must not overlap Production or Staging data roots") {
		t.Fatalf("cross-Environment backup namespace error = %s", output)
	}
}

func createBackupVolumeFixtures(t *testing.T, fixtureRoot, projectName string) {
	t.Helper()
	for _, serviceName := range backupFixtureServiceNames {
		volumeRoot := filepath.Join(fixtureRoot, projectName+"_"+serviceName+"-config")
		if err := os.MkdirAll(volumeRoot, 0o700); err != nil {
			t.Fatalf("create %s restore fixture: %v", serviceName, err)
		}
		if err := os.WriteFile(filepath.Join(volumeRoot, "identity.txt"), []byte(serviceName+"\n"), 0o600); err != nil {
			t.Fatalf("write %s restore fixture: %v", serviceName, err)
		}
	}
}

func backupConfig(t *testing.T, repository, temporary string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(repository, "stacks", "media", "media-stack.yaml"))
	if err != nil {
		t.Fatalf("read Declared Configuration: %v", err)
	}
	configured := strings.Replace(string(contents), "/mnt/backups/media/production", filepath.Join(temporary, "backups", "production"), 1)
	configured = strings.Replace(configured, "/mnt/backups/media/staging", filepath.Join(temporary, "backups", "staging"), 1)
	path := filepath.Join(temporary, "media-stack.yaml")
	if err := os.WriteFile(path, []byte(configured), 0o600); err != nil {
		t.Fatalf("write Declared Configuration: %v", err)
	}
	return path
}

func fakeDockerPath(t *testing.T, temporary string) string {
	t.Helper()
	directory := filepath.Join(temporary, "bin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create fake Docker directory: %v", err)
	}
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
case "$1" in
	volume)
		case "$2" in
			create)
				mkdir -p "$FAKE_DOCKER_FIXTURE_ROOT/$3"
				printf '%s\n' "$3"
				;;
			rm)
				for volume_name in "$@"; do
					:
				done
				rm -rf -- "$FAKE_DOCKER_FIXTURE_ROOT/$volume_name"
				;;
		esac
		;;
  compose)
    case " $* " in
      *" ps --status running --services "*)
        printf '%s\n' gluetun qbittorrent prowlarr sonarr radarr profilarr jellyfin seerr
        ;;
    esac
    ;;
  create)
    previous=""
    for argument in "$@"; do
      if [ "$previous" = "--volume" ]; then
        printf '%s\n' "${argument%%:*}"
        exit 0
      fi
      previous="$argument"
    done
    exit 1
    ;;
  cp)
	if [ "$2" = "-" ]; then
		container="${3%%:*}"
		mkdir -p "$FAKE_DOCKER_FIXTURE_ROOT/$container"
		tar -C "$FAKE_DOCKER_FIXTURE_ROOT/$container" -xf -
		exit 0
	fi
    container="${2%%:*}"
    if [ "${FAKE_DOCKER_FAIL_CP:-}" = "$container" ]; then
      exit 42
    fi
    tar -C "$FAKE_DOCKER_FIXTURE_ROOT/$container" -cf - .
    ;;
  rm)
    ;;
esac
`
	path := filepath.Join(directory, "docker")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Docker: %v", err)
	}
	return directory
}

func archiveFile(t *testing.T, archive []byte, name string) string {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			t.Fatalf("archive omitted %s", name)
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if strings.TrimPrefix(header.Name, "./") != name {
			continue
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read %s from archive: %v", name, err)
		}
		return string(contents)
	}
}

func backupCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	goArguments := append([]string{"run", "../../cmd/media-stack", "backup"}, arguments...)
	command := exec.Command("go", goArguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
	return command
}
