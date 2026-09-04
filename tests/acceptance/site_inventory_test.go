package acceptance_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type sitePlanOutput struct {
	APIVersion  string   `json:"apiVersion"`
	Kind        string   `json:"kind"`
	Site        string   `json:"site"`
	Instance    string   `json:"instance"`
	Host        string   `json:"host"`
	Stack       string   `json:"stack"`
	Environment string   `json:"environment"`
	Sources     []string `json:"sources"`
	Plan        string   `json:"plan"`
}

func TestExampleSiteValidatesThroughRepositoryCLI(t *testing.T) {
	command := homelabCommand(t, "site", "validate", "--site", "example")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("homelab site validate failed: %v\n%s", err, output)
	}

	want := "SITE_VALID valid Site example: 2 Hosts, 1 Stack Instance\n"
	if string(output) != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestSiteNamesResolveFromOutsideRepository(t *testing.T) {
	command := homelabCommand(t, "site", "validate", "--site", "example")
	command.Dir = t.TempDir()
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+repositoryRoot(t))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("homelab site validate failed outside repository: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "SITE_VALID") {
		t.Fatalf("output = %q, want successful Site validation", output)
	}
}

func TestSitePlanDelegatesToExistingMediaStackPlanner(t *testing.T) {
	legacy := planCommand(t, "--environment", "staging")
	legacyOutput, err := legacy.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy media-stack plan failed: %v\n%s", err, legacyOutput)
	}

	command := homelabCommand(t, "plan", "--site", "example", "--instance", "media", "--environment", "staging", "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("homelab plan failed: %v\n%s", err, output)
	}
	var got sitePlanOutput
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode homelab plan: %v\n%s", err, output)
	}
	if got.APIVersion != "homelab.site/v1alpha1" || got.Kind != "StackPlan" || got.Site != "example" || got.Instance != "media" || got.Host != "media" || got.Stack != "media" || got.Environment != "staging" {
		t.Fatalf("plan identity = %#v", got)
	}
	if got.Plan != string(legacyOutput) {
		t.Fatalf("Site-resolved plan differs from legacy plan\nsite:\n%s\nlegacy:\n%s", got.Plan, legacyOutput)
	}
	wantSources := []string{"sites/example/site.yaml", "sites/example/hosts/media.yaml", "sites/example/instances/media.yaml", "stacks/media/media-stack.yaml", "stacks/media/versions.yaml"}
	if len(got.Sources) != len(wantSources) {
		t.Fatalf("sources = %#v, want %#v", got.Sources, wantSources)
	}
	for index := range wantSources {
		if got.Sources[index] != wantSources[index] {
			t.Fatalf("sources = %#v, want %#v", got.Sources, wantSources)
		}
	}
}

func TestSitePlanIgnoresInvalidUnselectedStackInstance(t *testing.T) {
	fixture := newSiteFixture(t)
	invalid := secondMediaInstance(fixture.mediaInstance, true, true, true)
	invalid = strings.Replace(invalid, "stack: media", "stack: unsupported", 1)
	fixture.addInstance("invalid.yaml", invalid)
	root := fixture.write(t)
	command := homelabCommand(t, "plan", "--site", "fixture", "--instance", "media", "--environment", "staging", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unselected invalid Stack Instance blocked plan: %v\n%s", err, output)
	}
}

func TestJSONPlanFailureIsSchemaVersionedAndActionable(t *testing.T) {
	command := homelabCommand(t, "plan", "--site", "example", "--instance", "missing", "--environment", "staging", "--output", "json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Fatalf("missing Stack Instance unexpectedly planned: %s", stdout.String())
	}
	var failure struct {
		APIVersion  string `json:"apiVersion"`
		Kind        string `json:"kind"`
		Diagnostics []struct {
			Code   string `json:"code"`
			Remedy string `json:"remedy"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
		t.Fatalf("decode JSON plan failure: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if failure.APIVersion != "homelab.site/v1alpha1" || failure.Kind != "StackPlan" || len(failure.Diagnostics) != 1 || failure.Diagnostics[0].Code != "SITE_STACK_INSTANCE_MISSING" || failure.Diagnostics[0].Remedy == "" {
		t.Fatalf("plan failure = %#v", failure)
	}
}

func TestSiteValidationRejectsUnsafeOrAmbiguousDocuments(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*siteFixture)
		code   string
	}{
		{
			name: "missing explicit reference",
			mutate: func(fixture *siteFixture) {
				fixture.site = strings.Replace(fixture.site, "hosts/media.yaml", "hosts/missing.yaml", 1)
			},
			code: "SITE_REFERENCE_MISSING",
		},
		{
			name: "missing explicit Stack Instance reference",
			mutate: func(fixture *siteFixture) {
				fixture.site = strings.Replace(fixture.site, "instances/media.yaml", "instances/missing.yaml", 1)
			},
			code: "SITE_REFERENCE_MISSING",
		},
		{
			name: "duplicate Host identifier",
			mutate: func(fixture *siteFixture) {
				fixture.utilityHost = strings.Replace(fixture.utilityHost, "name: utility", "name: media", 1)
			},
			code: "SITE_IDENTIFIER_DUPLICATE",
		},
		{
			name: "duplicate Stack Instance identifier",
			mutate: func(fixture *siteFixture) {
				fixture.addInstance("duplicate.yaml", fixture.mediaInstance)
			},
			code: "SITE_IDENTIFIER_DUPLICATE",
		},
		{
			name: "unsupported API version",
			mutate: func(fixture *siteFixture) {
				fixture.mediaHost = strings.Replace(fixture.mediaHost, "homelab.site/v1alpha1", "homelab.site/v2", 1)
			},
			code: "SITE_DOCUMENT_UNSUPPORTED",
		},
		{
			name: "unsupported kind",
			mutate: func(fixture *siteFixture) {
				fixture.mediaHost = strings.Replace(fixture.mediaHost, "kind: Host", "kind: Machine", 1)
			},
			code: "SITE_DOCUMENT_UNSUPPORTED",
		},
		{
			name: "unknown field",
			mutate: func(fixture *siteFixture) {
				fixture.mediaHost += "unknownField: misspelled\n"
			},
			code: "SITE_DOCUMENT_UNKNOWN_FIELD",
		},
		{
			name: "traversal escape",
			mutate: func(fixture *siteFixture) {
				fixture.site = strings.Replace(fixture.site, "hosts/media.yaml", "../../outside.yaml", 1)
				fixture.outsideHost = fixture.mediaHost
			},
			code: "SITE_REFERENCE_ESCAPE",
		},
		{
			name: "symlink escape",
			mutate: func(fixture *siteFixture) {
				fixture.site = strings.Replace(fixture.site, "hosts/media.yaml", "hosts/link.yaml", 1)
				fixture.symlinkEscape = true
			},
			code: "SITE_REFERENCE_ESCAPE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSiteFixture(t)
			test.mutate(&fixture)
			root := fixture.write(t)
			command := homelabCommand(t, "site", "validate", "--site", "fixture", "--output", "json")
			command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("validation unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), `"code":"`+test.code+`"`) {
				t.Fatalf("output = %s, want diagnostic %s", output, test.code)
			}
		})
	}
}

func TestSiteValidationUsesOnlyExplicitReferences(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	writeFile(t, filepath.Join(root, "sites", "fixture", "hosts", "unreferenced.yaml"), []byte("not: a supported document\n"), 0o600)
	command := homelabCommand(t, "site", "validate", "--site", "fixture")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unreferenced document changed validation: %v\n%s", err, output)
	}
}

func TestSiteValidationEnforcesHostPlacementContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*siteFixture)
		code   string
	}{
		{
			name: "unknown capability",
			mutate: func(fixture *siteFixture) {
				fixture.mediaHost += "        - hardware.magic\n"
			},
			code: "SITE_CAPABILITY_UNKNOWN",
		},
		{
			name: "missing required capability",
			mutate: func(fixture *siteFixture) {
				fixture.mediaHost = strings.Replace(fixture.mediaHost, "        - network.tun\n", "", 1)
			},
			code: "SITE_CAPABILITY_MISSING",
		},
		{
			name: "missing Host binding",
			mutate: func(fixture *siteFixture) {
				fixture.mediaInstance = strings.Replace(fixture.mediaInstance, "host: media", "host: absent", 1)
			},
			code: "SITE_HOST_REFERENCE_MISSING",
		},
		{
			name: "unknown Stack",
			mutate: func(fixture *siteFixture) {
				fixture.mediaInstance = strings.Replace(fixture.mediaInstance, "stack: media", "stack: imaginary", 1)
			},
			code: "SITE_STACK_UNSUPPORTED",
		},
		{
			name: "same Host port collision",
			mutate: func(fixture *siteFixture) {
				fixture.addInstance("media-two.yaml", secondMediaInstance(fixture.mediaInstance, false, true, true))
			},
			code: "SITE_PORT_CONFLICT",
		},
		{
			name: "same Host storage collision",
			mutate: func(fixture *siteFixture) {
				fixture.addInstance("media-two.yaml", secondMediaInstance(fixture.mediaInstance, true, false, true))
			},
			code: "SITE_STORAGE_CONFLICT",
		},
		{
			name: "same Host runtime identity collision",
			mutate: func(fixture *siteFixture) {
				fixture.addInstance("media-two.yaml", secondMediaInstance(fixture.mediaInstance, true, true, false))
			},
			code: "SITE_RUNTIME_IDENTITY_CONFLICT",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSiteFixture(t)
			test.mutate(&fixture)
			root := fixture.write(t)
			command := homelabCommand(t, "site", "validate", "--site", "fixture", "--output", "json")
			command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("validation unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), `"code":"`+test.code+`"`) {
				t.Fatalf("output = %s, want diagnostic %s", output, test.code)
			}
		})
	}
}

func TestEquivalentResourcesAreValidOnDifferentHosts(t *testing.T) {
	fixture := newSiteFixture(t)
	fixture.utilityHost = strings.Replace(fixture.mediaHost, "name: media", "name: utility", 1)
	second := strings.Replace(fixture.mediaInstance, "name: media", "name: media-two", 1)
	second = strings.Replace(second, "host: media", "host: utility", 1)
	fixture.addInstance("media-two.yaml", second)
	root := fixture.write(t)
	command := homelabCommand(t, "site", "validate", "--site", "fixture")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("equivalent resources on distinct Hosts were rejected: %v\n%s", err, output)
	}
}

func TestDistinctStackInstancesAreValidOnOneHost(t *testing.T) {
	fixture := newSiteFixture(t)
	fixture.addInstance("media-two.yaml", secondMediaInstance(fixture.mediaInstance, true, true, true))
	root := fixture.write(t)
	command := homelabCommand(t, "site", "validate", "--site", "fixture")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("distinct Stack Instances on one Host were rejected: %v\n%s", err, output)
	}
}

func TestSiteNameCannotEscapeSiteCollection(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	command := homelabCommand(t, "site", "validate", "--site", "../fixture", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("escaping Site name was accepted:\n%s", output)
	}
	if !strings.Contains(string(output), `"code":"SITE_NAME_INVALID"`) {
		t.Fatalf("output = %s, want SITE_NAME_INVALID", output)
	}
}

func TestSiteOutputsAreDeterministicAndExplainSelection(t *testing.T) {
	first := homelabCommand(t, "site", "validate", "--site", "example", "--output", "json")
	firstOutput, err := first.CombinedOutput()
	if err != nil {
		t.Fatalf("first validation failed: %v\n%s", err, firstOutput)
	}
	second := homelabCommand(t, "site", "validate", "--site", "example", "--output", "json")
	secondOutput, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("second validation failed: %v\n%s", err, secondOutput)
	}
	if string(firstOutput) != string(secondOutput) {
		t.Fatalf("JSON validation output changed\nfirst: %s\nsecond: %s", firstOutput, secondOutput)
	}

	plan := homelabCommand(t, "plan", "--site", "example", "--instance", "media", "--environment", "staging", "--output", "human")
	planOutput, err := plan.CombinedOutput()
	if err != nil {
		t.Fatalf("human plan failed: %v\n%s", err, planOutput)
	}
	for _, want := range []string{"SITE_PLAN Site example, Stack Instance media, Host media, staging Environment", "source: sites/example/site.yaml", "source: stacks/media/media-stack.yaml", "---\nname: media-staging"} {
		if !strings.Contains(string(planOutput), want) {
			t.Fatalf("human plan omitted %q:\n%s", want, planOutput)
		}
	}
}

func TestSitesMayReuseLocalIdentifiers(t *testing.T) {
	fixture := newSiteFixture(t)
	root := fixture.write(t)
	sourceRoot := filepath.Join(root, "sites", "fixture")
	secondRoot := filepath.Join(root, "sites", "parents")
	if err := os.MkdirAll(filepath.Join(secondRoot, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(secondRoot, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(secondRoot, "site.yaml"), []byte(strings.Replace(fixture.site, "name: fixture", "name: parents", 1)), 0o600)
	for _, relative := range []string{"hosts/media.yaml", "hosts/utility.yaml", "instances/media.yaml"} {
		copyFile(t, filepath.Join(sourceRoot, filepath.FromSlash(relative)), filepath.Join(secondRoot, filepath.FromSlash(relative)), 0o600)
	}
	for _, siteName := range []string{"fixture", "parents"} {
		command := homelabCommand(t, "site", "validate", "--site", siteName)
		command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Site %s could not reuse local identifiers: %v\n%s", siteName, err, output)
		}
	}
}

func TestSitePlanDecryptsSelectedSiteTopologyAndRedactsOutput(t *testing.T) {
	dailyIdentity, dailyRecipient := newAgeIdentity(t)
	recoveryIdentity, recoveryRecipient := newAgeIdentity(t)
	fixture := newSiteFixture(t)
	fixture.site += "    sensitiveValues:\n        document: secrets/site.sops.yaml\n        dailyRecipient: " + dailyRecipient + "\n        recoveryRecipient: " + recoveryRecipient + "\n"
	fixture.mediaInstance += sensitiveStagingReferences("/private/family-media/staging", "/private/family-backups/staging")
	root := fixture.write(t)
	secretPath := filepath.Join(root, "sites", "fixture", "secrets", "site.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	plaintext := "apiVersion: homelab.site/v1alpha1\nkind: SiteSensitiveValues\nspec:\n    values:\n        - id: staging-data-root\n          value: /private/family-media/staging\n        - id: staging-backup-root\n          value: /private/family-backups/staging\n"
	encryptSiteValues(t, secretPath, plaintext, dailyRecipient, recoveryRecipient)

	for _, identity := range []string{dailyIdentity, recoveryIdentity} {
		command := homelabCommand(t, "plan", "--site", "fixture", "--instance", "media", "--environment", "staging", "--output", "json")
		command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("authorized Site plan failed: %v\n%s", err, output)
		}
		for _, forbidden := range []string{"/private/family-media", "/private/family-backups", "ENC["} {
			if strings.Contains(string(output), forbidden) {
				t.Fatalf("Site plan disclosed %q:\n%s", forbidden, output)
			}
		}
		for _, reference := range []string{"staging-data-root", "staging-backup-root", "<redacted:staging-data-root>"} {
			if !strings.Contains(string(output), reference) {
				t.Fatalf("Site plan omitted redacted reference %q:\n%s", reference, output)
			}
		}
	}

	unrelatedIdentity := filepath.Join(t.TempDir(), "unrelated.txt")
	unrelated := exec.Command("age-keygen", "-o", unrelatedIdentity)
	if output, err := unrelated.CombinedOutput(); err != nil {
		t.Fatalf("create unrelated identity: %v\n%s", err, output)
	}
	command := homelabCommand(t, "plan", "--site", "fixture", "--instance", "media", "--environment", "staging", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+unrelatedIdentity)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("unrelated Site identity decrypted plan:\n%s", output)
	}
	if !strings.Contains(string(output), "SITE_SENSITIVE_VALUES_DECRYPT_FAILED") || strings.Contains(string(output), "/private/") || strings.Contains(string(output), "ENC[") {
		t.Fatalf("unsafe or unstable decryption failure:\n%s", output)
	}

	otherDailyIdentity, otherDailyRecipient := newAgeIdentity(t)
	otherRecoveryIdentity, otherRecoveryRecipient := newAgeIdentity(t)
	parentsRoot := filepath.Join(root, "sites", "parents")
	if err := os.MkdirAll(filepath.Join(parentsRoot, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parentsRoot, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	parentsSite := strings.Replace(fixture.site, "name: fixture", "name: parents", 1)
	parentsSite = strings.Replace(parentsSite, dailyRecipient, otherDailyRecipient, 1)
	parentsSite = strings.Replace(parentsSite, recoveryRecipient, otherRecoveryRecipient, 1)
	writeFile(t, filepath.Join(parentsRoot, "site.yaml"), []byte(parentsSite), 0o600)
	writeFile(t, filepath.Join(parentsRoot, "hosts", "media.yaml"), []byte(fixture.mediaHost), 0o600)
	writeFile(t, filepath.Join(parentsRoot, "hosts", "utility.yaml"), []byte(fixture.utilityHost), 0o600)
	writeFile(t, filepath.Join(parentsRoot, "instances", "media.yaml"), []byte(fixture.mediaInstance), 0o600)
	parentsSecret := filepath.Join(parentsRoot, "secrets", "site.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(parentsSecret), 0o700); err != nil {
		t.Fatal(err)
	}
	encryptSiteValues(t, parentsSecret, plaintext, otherDailyRecipient, otherRecoveryRecipient)
	for _, check := range []struct {
		identity string
		wantOK   bool
	}{{dailyIdentity, false}, {otherDailyIdentity, true}, {otherRecoveryIdentity, true}} {
		command := homelabCommand(t, "plan", "--site", "parents", "--instance", "media", "--environment", "staging", "--output", "json")
		command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+check.identity)
		output, err := command.CombinedOutput()
		if check.wantOK && err != nil {
			t.Fatalf("authorized Parents Site identity failed: %v\n%s", err, output)
		}
		if !check.wantOK && (err == nil || !strings.Contains(string(output), "SITE_SENSITIVE_VALUES_DECRYPT_FAILED")) {
			t.Fatalf("Home daily identity accessed Parents Site:\n%s", output)
		}
	}
}

func TestSiteValidationChecksRecipientsAndEncryptedStorageClaims(t *testing.T) {
	_, dailyRecipient := newAgeIdentity(t)
	_, recoveryRecipient := newAgeIdentity(t)
	_, undeclaredRecipient := newAgeIdentity(t)
	fixture := newSiteFixture(t)
	fixture.site += "    sensitiveValues:\n        document: secrets/site.sops.yaml\n        dailyRecipient: " + dailyRecipient + "\n        recoveryRecipient: " + undeclaredRecipient + "\n"
	fixture.mediaInstance += sensitiveStagingReferences("/private/data", "/private/backups")
	second := secondMediaInstance(fixture.mediaInstance, true, true, true)
	fixture.addInstance("media-two.yaml", second)
	root := fixture.write(t)
	secretPath := filepath.Join(root, "sites", "fixture", "secrets", "site.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	plaintext := "apiVersion: homelab.site/v1alpha1\nkind: SiteSensitiveValues\nspec:\n    values:\n        - id: staging-data-root\n          value: /private/data\n        - id: staging-backup-root\n          value: /private/backups\n"
	encryptSiteValues(t, secretPath, plaintext, dailyRecipient, recoveryRecipient)
	command := homelabCommand(t, "site", "validate", "--site", "fixture", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("recipient mismatch and storage collision were accepted:\n%s", output)
	}
	for _, code := range []string{"SITE_SENSITIVE_RECIPIENTS_MISMATCH", "SITE_STORAGE_CONFLICT"} {
		if !strings.Contains(string(output), `"code":"`+code+`"`) {
			t.Fatalf("validation omitted %s:\n%s", code, output)
		}
	}
}

func TestSiteValidationRejectsNestedEncryptedStorageClaims(t *testing.T) {
	fixture := newSiteFixture(t)
	fixture.mediaInstance += sensitiveStagingReferences("/private/media", "/private/backups")
	second := secondMediaInstance(fixture.mediaInstance, true, true, true)
	second = strings.Replace(second, sensitiveStagingReferences("/private/media", "/private/backups"), sensitiveStagingReferences("/private/media/movies", "/private/other-backups"), 1)
	fixture.addInstance("media-two.yaml", second)
	root := fixture.write(t)

	command := homelabCommand(t, "site", "validate", "--site", "fixture", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("nested encrypted storage claims were accepted:\n%s", output)
	}
	if !strings.Contains(string(output), `"code":"SITE_STORAGE_CONFLICT"`) {
		t.Fatalf("validation omitted nested storage conflict:\n%s", output)
	}
}

func TestSitePlanRejectsStorageClaimThatDoesNotMatchDecryptedRoot(t *testing.T) {
	identity, dailyRecipient := newAgeIdentity(t)
	_, recoveryRecipient := newAgeIdentity(t)
	fixture := newSiteFixture(t)
	fixture.site += "    sensitiveValues:\n        document: secrets/site.sops.yaml\n        dailyRecipient: " + dailyRecipient + "\n        recoveryRecipient: " + recoveryRecipient + "\n"
	fixture.mediaInstance += sensitiveStagingReferences("/private/different-data", "/private/backups")
	root := fixture.write(t)
	secretPath := filepath.Join(root, "sites", "fixture", "secrets", "site.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	plaintext := "apiVersion: homelab.site/v1alpha1\nkind: SiteSensitiveValues\nspec:\n    values:\n        - id: staging-data-root\n          value: /private/actual-data\n        - id: staging-backup-root\n          value: /private/backups\n"
	encryptSiteValues(t, secretPath, plaintext, dailyRecipient, recoveryRecipient)
	command := homelabCommand(t, "plan", "--site", "fixture", "--instance", "media", "--environment", "staging", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("mismatched storage claim was accepted:\n%s", output)
	}
	if !strings.Contains(string(output), "SITE_STORAGE_CLAIM_MISMATCH") || strings.Contains(string(output), "/private/") {
		t.Fatalf("unsafe or unstable storage-claim failure:\n%s", output)
	}
}

func TestSiteValidationDoesNotCallLiveOrSecretServices(t *testing.T) {
	dailyIdentity, dailyRecipient := newAgeIdentity(t)
	_, recoveryRecipient := newAgeIdentity(t)
	fixture := newSiteFixture(t)
	fixture.site += "    sensitiveValues:\n        document: secrets/site.sops.yaml\n        dailyRecipient: " + dailyRecipient + "\n        recoveryRecipient: " + recoveryRecipient + "\n"
	root := fixture.write(t)
	secretPath := filepath.Join(root, "sites", "fixture", "secrets", "site.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	plaintext := "apiVersion: homelab.site/v1alpha1\nkind: SiteSensitiveValues\nspec:\n    values:\n        - id: unused\n          value: /private/unused\n"
	encryptSiteValues(t, secretPath, plaintext, dailyRecipient, recoveryRecipient)
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls")
	for _, name := range []string{"sops", "docker"} {
		writeFile(t, filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$SITE_CALL_LOG\"\nexit 70\n"), 0o700)
	}
	command := homelabCommand(t, "site", "validate", "--site", "fixture")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+dailyIdentity, "SITE_CALL_LOG="+logPath, "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("offline structural validation failed: %v\n%s", err, output)
	}
	if contents, err := os.ReadFile(logPath); err == nil {
		t.Fatalf("structural validation made external calls:\n%s", contents)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func newAgeIdentity(t *testing.T) (string, string) {
	t.Helper()
	identity := filepath.Join(t.TempDir(), "identity.txt")
	command := exec.Command("age-keygen", "-o", identity)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create age identity: %v\n%s", err, output)
	}
	command = exec.Command("age-keygen", "-y", identity)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("derive age recipient: %v", err)
	}
	return identity, strings.TrimSpace(string(output))
}

func encryptSiteValues(t *testing.T, path, plaintext string, recipients ...string) {
	t.Helper()
	arguments := []string{"encrypt", "--input-type", "yaml", "--output-type", "yaml", "--encrypted-regex", "^value$", "--age", strings.Join(recipients, ",")}
	arguments = append(arguments, "/dev/stdin")
	command := exec.Command("sops", arguments...)
	command.Stdin = strings.NewReader(plaintext)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("encrypt Site values: %v", err)
	}
	writeFile(t, path, output, 0o600)
}

func sensitiveStagingReferences(dataRoot, backupRoot string) string {
	return "        sensitiveReferences:\n            environments:\n                staging:\n                    dataRoot: staging-data-root\n" + storageClaimYAML("dataRoot", dataRoot) + "                    backupRoot: staging-backup-root\n" + storageClaimYAML("backupRoot", backupRoot)
}

func storageClaimYAML(field, path string) string {
	claim := func(value string) string {
		digest := sha256.Sum256([]byte(filepath.Clean(value)))
		return fmt.Sprintf("sha256:%x", digest)
	}
	result := "                    " + field + "Claim: " + claim(path) + "\n"
	ancestors := []string{}
	for parent := filepath.Dir(filepath.Clean(path)); parent != string(filepath.Separator) && parent != "."; parent = filepath.Dir(parent) {
		ancestors = append(ancestors, claim(parent))
	}
	if len(ancestors) > 0 {
		result += "                    " + field + "AncestorClaims:\n"
		for _, ancestor := range ancestors {
			result += "                        - " + ancestor + "\n"
		}
	}
	return result
}

type siteFixture struct {
	site           string
	mediaHost      string
	utilityHost    string
	mediaInstance  string
	outsideHost    string
	symlinkEscape  bool
	extraInstances map[string]string
}

func newSiteFixture(t *testing.T) siteFixture {
	t.Helper()
	read := func(path ...string) string {
		contents, err := os.ReadFile(filepath.Join(append([]string{repositoryRoot(t)}, path...)...))
		if err != nil {
			t.Fatalf("read fixture source: %v", err)
		}
		return string(contents)
	}
	return siteFixture{
		site:           strings.Replace(read("sites", "example", "site.yaml"), "name: example", "name: fixture", 1),
		mediaHost:      read("sites", "example", "hosts", "media.yaml"),
		utilityHost:    read("sites", "example", "hosts", "utility.yaml"),
		mediaInstance:  read("sites", "example", "instances", "media.yaml"),
		extraInstances: map[string]string{},
	}
}

func (fixture *siteFixture) addInstance(filename, contents string) {
	fixture.site = strings.Replace(fixture.site, "        - instances/media.yaml", "        - instances/media.yaml\n        - instances/"+filename, 1)
	fixture.extraInstances[filename] = contents
}

func secondMediaInstance(source string, distinctPorts, distinctStorage, distinctRuntime bool) string {
	result := strings.Replace(source, "name: media", "name: media-two", 1)
	result = strings.Replace(result, "projectName: media-", "projectName: media-two-", -1)
	if distinctRuntime {
		result = strings.Replace(result, "runtimeIdentity: example-media", "runtimeIdentity: example-media-two", 1)
	}
	if distinctStorage {
		result = strings.Replace(result, "/srv/media/", "/srv/media-two/", -1)
		result = strings.Replace(result, "/mnt/backups/media/", "/mnt/backups/media-two/", -1)
	}
	if distinctPorts {
		replacements := map[string]string{
			"8080": "28080", "9696": "29696", "8989": "28989", "7878": "27878", "6868": "26868", "8096": "28096", "5055": "25055",
			"18080": "38080", "19696": "39696", "18989": "38989", "17878": "37878", "16868": "36868", "18096": "38096", "15055": "35055",
		}
		for old, replacement := range replacements {
			result = strings.Replace(result, ": "+old+"\n", ": "+replacement+"\n", -1)
		}
	}
	return result
}

func (fixture siteFixture) write(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	siteRoot := filepath.Join(root, "sites", "fixture")
	if err := os.MkdirAll(filepath.Join(siteRoot, "hosts"), 0o700); err != nil {
		t.Fatalf("create Host fixture directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(siteRoot, "instances"), 0o700); err != nil {
		t.Fatalf("create Stack Instance fixture directory: %v", err)
	}
	writeFile(t, filepath.Join(siteRoot, "site.yaml"), []byte(fixture.site), 0o600)
	writeFile(t, filepath.Join(siteRoot, "hosts", "media.yaml"), []byte(fixture.mediaHost), 0o600)
	writeFile(t, filepath.Join(siteRoot, "hosts", "utility.yaml"), []byte(fixture.utilityHost), 0o600)
	writeFile(t, filepath.Join(siteRoot, "instances", "media.yaml"), []byte(fixture.mediaInstance), 0o600)
	for filename, contents := range fixture.extraInstances {
		writeFile(t, filepath.Join(siteRoot, "instances", filename), []byte(contents), 0o600)
	}
	stackRoot := filepath.Join(root, "stacks", "media")
	if err := os.MkdirAll(stackRoot, 0o700); err != nil {
		t.Fatalf("create Stack fixture directory: %v", err)
	}
	copyFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml"), filepath.Join(stackRoot, "media-stack.yaml"), 0o600)
	copyFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "versions.yaml"), filepath.Join(stackRoot, "versions.yaml"), 0o600)
	if fixture.outsideHost != "" {
		writeFile(t, filepath.Join(root, "outside.yaml"), []byte(fixture.outsideHost), 0o600)
	}
	if fixture.symlinkEscape {
		outside := filepath.Join(root, "outside-link-target.yaml")
		writeFile(t, outside, []byte(fixture.mediaHost), 0o600)
		if err := os.Symlink(outside, filepath.Join(siteRoot, "hosts", "link.yaml")); err != nil {
			t.Fatalf("create escaping symlink: %v", err)
		}
	}
	return root
}

func homelabCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "homelab")
	build := exec.Command("go", "build", "-o", binary, "./cmd/homelab")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build homelab CLI: %v\n%s", err, output)
	}
	command := exec.Command(binary, arguments...)
	command.Dir = filepath.Join(repositoryRoot(t), "stacks", "media")
	return command
}
