package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMediaCommandsRequireExplicitDeployment(t *testing.T) {
	for _, operation := range []string{"plan", "apply", "doctor", "backup", "restore", "promote", "verify", "init"} {
		t.Run(operation, func(t *testing.T) {
			environment := "staging"
			if operation == "promote" {
				environment = "production"
			}
			args := []string{"run", "./cmd/media-stack", operation, "--environment", environment}
			if operation == "restore" {
				args = append(args, "--backup", "unused.json")
			}
			if operation == "promote" {
				args = append(args, "--verification", "unused.json", "--backup", "unused.json")
			}
			if operation == "verify" {
				args = append(args, "--suite", "companions")
			}
			command := exec.Command("go", args...)
			command.Dir = repositoryRoot(t)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "--site and --instance") {
				t.Fatalf("missing deployment must fail with selector guidance: %v\n%s", err, output)
			}
		})
	}
}

func TestScopedCompanionWritebackPreservesOtherCredentialDocuments(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	identity, recipient := newAgeIdentity(t)
	base := filepath.Join(root, "sites/fixture/secrets")
	vpn := filepath.Join(base, "nordvpn.sops.yaml")
	encryptScopedFixture(t, vpn, recipient, "site: fixture\nnordvpn:\n  openvpn:\n    serviceUsername: shared-service\n    servicePassword: shared-password\n")
	production := filepath.Join(base, "media/production.sops.yaml")
	encryptScopedFixture(t, production, recipient, "scope:\n  site: fixture\n  instance: media\n  environment: production\n"+scopedApplicationCredentials)
	staging := filepath.Join(base, "media/staging.sops.yaml")
	encryptScopedFixture(t, staging, recipient, "scope:\n  site: fixture\n  instance: media\n  environment: staging\n"+strings.Split(scopedApplicationCredentials, "cleanuparr:")[0])
	vpnBefore, productionBefore := readFile(t, vpn), readFile(t, production)
	answers := filepath.Join(root, "companion-answers.yaml")
	writeFile(t, answers, []byte("cleanuparrUsername: chosen-clean\ncleanuparrPassword: chosen-clean-password\n"), 0600)
	command := scopedMediaCommand(t, root, identity, "init", "--companions", "--non-interactive", "--answers", answers)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("scoped companion writeback: %v\n%s", err, output)
	}
	if string(vpnBefore) != string(readFile(t, vpn)) || string(productionBefore) != string(readFile(t, production)) {
		t.Fatal("companion writeback changed another credential document")
	}
	decrypt := exec.Command("sops", "decrypt", "--output-type", "yaml", staging)
	decrypt.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity)
	plain, err := decrypt.Output()
	if err != nil || !strings.Contains(string(plain), "chosen-clean-password") || !strings.Contains(string(plain), "preserve-profilarr") || !strings.Contains(string(plain), "instance: media") {
		t.Fatal("companion writeback lost credentials or ownership")
	}
}

func TestScopedRestoreDrillRejectsAnotherEnvironmentsCredentials(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	identity, recipient := newAgeIdentity(t)
	credentials := filepath.Join(root, "production-copy.sops.yaml")
	encryptScopedFixture(t, credentials, recipient, "scope:\n  site: fixture\n  instance: media\n  environment: production\n"+scopedApplicationCredentials)
	command := scopedMediaCommand(t, root, identity, "restore", "--as-restore-drill", "--confirm-integrations", "--credentials", credentials)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "ownership") {
		t.Fatalf("Restore Drill accepted Production credentials: %v\n%s", err, output)
	}
}

func TestSiteRejectsApplicationSecretReuseAcrossHosts(t *testing.T) {
	fixture := newSiteFixture(t)
	second := secondMediaInstance(fixture.mediaInstance, true, true, true)
	second = strings.Replace(second, "host: media", "host: utility", 1)
	second = strings.ReplaceAll(second, "secrets/media-two/", "secrets/media/")
	fixture.addInstance("media-two.yaml", second)
	root := fixture.write(t)
	command := homelabCommand(t, "site", "validate", "--site", "fixture", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "SITE_SECRET_CONFLICT") {
		t.Fatalf("shared application secret document accepted across Hosts: %v\n%s", err, output)
	}
}

func TestSitePlansScopeRuntimeCredentialsByInstance(t *testing.T) {
	fixture := newSiteFixture(t)
	fixture.addInstance("media-two.yaml", secondMediaInstance(fixture.mediaInstance, true, true, true))
	root := fixture.write(t)
	for _, instance := range []string{"media", "media-two"} {
		command := homelabCommand(t, "plan", "--site", "fixture", "--instance", instance, "--environment", "staging")
		command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "media-stack/fixture/"+instance+"/staging/") {
			t.Fatalf("runtime credentials not scoped to instance: %v\n%s", err, output)
		}
	}
}

func TestSitePlanningRejectsCrossInstanceCredentialAndProjectCollisions(t *testing.T) {
	for _, scenario := range []string{"credentials", "project"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newSiteFixture(t)
			second := secondMediaInstance(fixture.mediaInstance, true, true, true)
			code := "SITE_SECRET_CONFLICT"
			if scenario == "credentials" {
				second = strings.ReplaceAll(second, "secrets/media-two/", "secrets/media/")
			} else {
				second = strings.ReplaceAll(second, "projectName: media-two-", "projectName: media-")
				code = "SITE_PROJECT_CONFLICT"
			}
			fixture.addInstance("media-two.yaml", second)
			root := fixture.write(t)
			command := homelabCommand(t, "plan", "--site", "fixture", "--instance", "media", "--environment", "staging", "--output", "json")
			command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), code) {
				t.Fatalf("planning accepted %s collision: %v\n%s", scenario, err, output)
			}
		})
	}
}

func TestScopedPromotionPreservesIdentityWithSensitiveStorageReferences(t *testing.T) {
	identity, recipient := newAgeIdentity(t)
	fixture := newSiteFixture(t)
	fixture.site += "    sensitiveValues:\n        document: secrets/site.sops.yaml\n        dailyRecipient: " + recipient + "\n"
	backupRoot := filepath.Join(t.TempDir(), "staging")
	fixture.mediaInstance += sensitiveStagingReferences("/private/scoped-media/staging", backupRoot)
	root := fixture.write(t)
	values := "apiVersion: homelab.site/v1alpha1\nkind: SiteSensitiveValues\nspec:\n  values:\n    - id: staging-data-root\n      value: /private/scoped-media/staging\n    - id: staging-backup-root\n      value: " + backupRoot + "\n"
	encryptScopedFixture(t, filepath.Join(root, "sites/fixture/secrets/site.sops.yaml"), recipient, values)
	volumeRoot := t.TempDir()
	createBackupVolumeFixtures(t, volumeRoot, "media-staging")
	bin := fakeDockerPath(t, t.TempDir())
	command := scopedMediaCommand(t, root, identity, "backup", "--output", "json")
	command.Env = append(command.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_DOCKER_FIXTURE_ROOT="+volumeRoot, "FAKE_DOCKER_LOG="+filepath.Join(root, "docker.log"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("scoped backup: %v\n%s", err, output)
	}
	var backup map[string]any
	if err := json.Unmarshal(output, &backup); err != nil {
		t.Fatal(err)
	}
	var diagnostics []map[string]string
	for _, code := range []string{"VERIFY_COMPANION_POLICY_CONVERGED", "VERIFY_EPISODE_SEARCH_DISPATCHED", "VERIFY_SERIES_RECOVERY_PROVEN", "VERIFY_SERIES_REQUESTED", "VERIFY_SERIES_EPISODE_ACQUIRED", "VERIFY_SERIES_EPISODE_HARDLINKED", "VERIFY_SERIES_EPISODE_PLAYBACK_READY"} {
		diagnostics = append(diagnostics, map[string]string{"code": code, "status": "pass"})
	}
	// The adapter supplies successful proof evidence; its configuration identity
	// comes from the actual selected Staging command, as verification's does.
	proof := map[string]any{"schemaVersion": "homelab.media-stack/verify/v1alpha1", "environment": "staging", "suite": "promotion", "configDigest": backup["configDigest"], "versionsDigest": backup["versionDigest"], "diagnostics": diagnostics}
	encoded, _ := json.Marshal(proof)
	proofPath := filepath.Join(root, "verification.json")
	writeFile(t, proofPath, encoded, 0600)
	command = exec.Command("go", "run", "./cmd/media-stack", "promote", "--site", "fixture", "--instance", "media", "--environment", "production", "--verification", proofPath, "--backup", backup["manifestPath"].(string))
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unchanged selected instance cannot promote with sensitive storage references: %v\n%s", err, output)
	}
}

func TestScopedBackupRecordsOwnershipAndRejectsCrossInstanceRestore(t *testing.T) {
	fixture := newSiteFixture(t)
	fixture.mediaInstance = strings.ReplaceAll(fixture.mediaInstance, "/mnt/backups/media/", t.TempDir()+"/")
	root := fixture.write(t)
	volumeRoot := t.TempDir()
	createBackupVolumeFixtures(t, volumeRoot, "media-staging")
	bin := fakeDockerPath(t, t.TempDir())
	command := scopedMediaCommand(t, root, "", "backup", "--output", "json")
	command.Env = append(command.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_DOCKER_FIXTURE_ROOT="+volumeRoot, "FAKE_DOCKER_LOG="+filepath.Join(root, "docker.log"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("scoped backup: %v\n%s", err, output)
	}
	var manifest map[string]any
	if err := json.Unmarshal(output, &manifest); err != nil {
		t.Fatal(err)
	}
	scope, ok := manifest["scope"].(map[string]any)
	if !ok || scope["site"] != "fixture" || scope["instance"] != "media" || scope["environment"] != "staging" {
		t.Fatal("backup omitted deployment ownership")
	}
	scope["instance"] = "another-instance"
	path := manifest["manifestPath"].(string)
	encoded, _ := json.Marshal(manifest)
	writeFile(t, path, encoded, 0600)
	command = scopedMediaCommand(t, root, "", "restore", "--backup", path)
	output, err = command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "ownership") {
		t.Fatalf("restore accepted another instance's backup: %v\n%s", err, output)
	}
}

func TestScopedInitRejectsCredentialsCopiedFromAnotherInstance(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	identity, recipient := newAgeIdentity(t)
	path := filepath.Join(root, "sites/fixture/secrets/media/staging.sops.yaml")
	encryptScopedFixture(t, path, recipient, "scope:\n  site: fixture\n  instance: another-machine\n  environment: staging\n"+scopedApplicationCredentials)
	command := scopedMediaCommand(t, root, identity, "init", "--non-interactive", "--answers", "unused.yaml")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "ownership") {
		t.Fatalf("copied credentials must fail ownership validation: %v\n%s", err, output)
	}
}

func TestScopedApplyRejectsWrongOwnershipBeforeDocker(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	policyDirectory := filepath.Join(root, "services/media/fixtures")
	if err := os.MkdirAll(policyDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"profilarr-movie-policy.yaml", "profilarr-series-policy.yaml"} {
		copyFile(t, filepath.Join(repositoryRoot(t), "services/media/fixtures", name), filepath.Join(policyDirectory, name), 0600)
	}
	identity, recipient := newAgeIdentity(t)
	path := filepath.Join(root, "sites/fixture/secrets/media/staging.sops.yaml")
	encryptScopedFixture(t, path, recipient, "scope:\n  site: fixture\n  instance: another-machine\n  environment: staging\n"+scopedApplicationCredentials)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "unexpected-docker.log")
	writeExecutable(t, filepath.Join(bin, "docker"), "#!/bin/sh\nprintf called >> \"$SCOPED_DOCKER_LOG\"\nexit 99\n")
	for _, operation := range []string{"apply", "verify"} {
		t.Run(operation, func(t *testing.T) {
			command := scopedMediaCommand(t, root, identity, operation)
			command.Env = append(command.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "SCOPED_DOCKER_LOG="+log)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "ownership") {
				t.Fatalf("operation did not reject copied credentials: %v\n%s", err, output)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("operation invoked Docker before ownership validation")
			}
		})
	}
}

func TestScopedInitPreservesSharedVPNAndExistingApplicationCredentials(t *testing.T) {
	fixture := newSiteFixture(t)
	fixture.mediaInstance = strings.ReplaceAll(fixture.mediaInstance, "/srv/media/", t.TempDir()+"/")
	root := fixture.write(t)
	identity, recipient := newAgeIdentity(t)
	vpnPath := filepath.Join(root, "sites/fixture/secrets/nordvpn.sops.yaml")
	encryptScopedFixture(t, vpnPath, recipient, "site: fixture\nnordvpn:\n  openvpn:\n    serviceUsername: shared-service\n    servicePassword: shared-password\n")
	vpnBefore := readFile(t, vpnPath)
	answersPath := filepath.Join(root, "answers.yaml")
	answers := strings.Replace(string(completeAnswers(strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), "Canada", "udp", "", "")), "age1example", recipient, -1)
	var values map[string]any
	if err := yaml.Unmarshal([]byte(answers), &values); err != nil {
		t.Fatal(err)
	}
	values["ageRecipient"] = recipient
	encoded, _ := yaml.Marshal(values)
	writeFile(t, answersPath, encoded, 0600)
	command := scopedMediaCommand(t, root, identity, "init", "--non-interactive", "--answers", answersPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize scoped credentials: %v\n%s", err, output)
	}
	applicationPath := filepath.Join(root, "sites/fixture/secrets/media/staging.sops.yaml")
	applicationBefore := readFile(t, applicationPath)
	decrypt := exec.Command("sops", "decrypt", "--output-type", "yaml", applicationPath)
	decrypt.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity)
	plain, err := decrypt.Output()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(plain, &document); err != nil {
		t.Fatal(err)
	}
	scope, ok := document["scope"].(map[string]any)
	if !ok || scope["site"] != "fixture" || scope["instance"] != "media" || scope["environment"] != "staging" || document["nordvpn"] != nil {
		t.Fatalf("application credentials must have deployment ownership and no VPN credentials")
	}
	command = scopedMediaCommand(t, root, identity, "init", "--non-interactive", "--answers", "unused.yaml")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("repeat initialization: %v\n%s", err, output)
	}
	if string(vpnBefore) != string(readFile(t, vpnPath)) || string(applicationBefore) != string(readFile(t, applicationPath)) {
		t.Fatal("repeat init changed encrypted credentials")
	}
}

func TestScopedMigrationPreservesCredentialsAndRefusesConflictingDestinations(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	identity, recipient := newAgeIdentity(t)
	legacyConfig := filepath.Join(root, "legacy/media-stack.yaml")
	if err := os.MkdirAll(filepath.Dir(legacyConfig), 0700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(repositoryRoot(t), "tests/fixtures/media-stack.yaml"), legacyConfig, 0600)
	legacySecret := filepath.Join(root, "legacy/secrets/staging.sops.yaml")
	encryptScopedFixture(t, legacySecret, recipient, "nordvpn:\n  openvpn:\n    serviceUsername: shared-service\n    servicePassword: shared-password\n"+scopedApplicationCredentials)
	before := readFile(t, legacySecret)
	run := func() ([]byte, error) {
		command := exec.Command("go", "run", "./cmd/media-stack", "migrate-secrets", "--site", "fixture", "--instance", "media", "--legacy-config", legacyConfig)
		command.Dir = repositoryRoot(t)
		command.Env = append(os.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
		return command.CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("migrate credentials: %v\n%s", err, output)
	}
	applicationPath := filepath.Join(root, "sites/fixture/secrets/media/staging.sops.yaml")
	applicationBefore := readFile(t, applicationPath)
	vpnPath := filepath.Join(root, "sites/fixture/secrets/nordvpn.sops.yaml")
	vpnBefore := readFile(t, vpnPath)
	for _, path := range []string{applicationPath, vpnPath} {
		command := exec.Command("sops", "decrypt", "--output-type", "yaml", path)
		command.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity)
		plain, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		if path == applicationPath && (!strings.Contains(string(plain), "preserve-profilarr-key-32-characters") || strings.Contains(string(plain), "shared-password")) {
			t.Fatal("application migration changed credentials or retained VPN")
		}
		if path == vpnPath && (!strings.Contains(string(plain), "shared-password") || strings.Contains(string(plain), "preserve-profilarr")) {
			t.Fatal("VPN migration changed credentials or retained applications")
		}
	}
	if output, err := run(); err != nil {
		t.Fatalf("repeat migration: %v\n%s", err, output)
	}
	if string(before) != string(readFile(t, legacySecret)) || string(applicationBefore) != string(readFile(t, applicationPath)) || string(vpnBefore) != string(readFile(t, vpnPath)) {
		t.Fatal("migration rewrote existing credentials")
	}
	encryptScopedFixture(t, applicationPath, recipient, "scope:\n  site: fixture\n  instance: another-instance\n  environment: staging\n"+scopedApplicationCredentials)
	conflicting := readFile(t, applicationPath)
	if output, err := run(); err == nil || !strings.Contains(string(output), "existing") {
		t.Fatalf("must refuse conflicting destination: %v\n%s", err, output)
	}
	if string(conflicting) != string(readFile(t, applicationPath)) {
		t.Fatal("migration replaced conflicting credentials")
	}
}

const scopedApplicationCredentials = `profilarr:
  apiKey: preserve-profilarr-key-32-characters
jellyfin:
  username: preserve-jellyfin
  password: preserve-jellyfin-password
qbittorrent:
  username: preserve-qbit
  password: preserve-qbit-password
houndarr:
  username: preserve-hound
  password: preserve-hound-password
cleanuparr:
  username: preserve-clean
  password: preserve-clean-password
`

func encryptScopedFixture(t *testing.T, path, recipient, plaintext string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sops", "encrypt", "--age", recipient, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	command.Stdin = strings.NewReader(plaintext)
	encrypted, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, encrypted, 0600)
}

func scopedMediaCommand(t *testing.T, root, identity, operation string, arguments ...string) *exec.Cmd {
	t.Helper()
	args := []string{"run", "./cmd/media-stack", operation, "--site", "fixture", "--instance", "media", "--environment", "staging"}
	command := exec.Command("go", append(args, arguments...)...)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
	return command
}
