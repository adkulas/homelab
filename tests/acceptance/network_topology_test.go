package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkTopologyValidatesWithOneSharedAgeIdentity(t *testing.T) {
	root, identity, site := networkTopologyFixture(t, nil)
	key := exec.Command("age-keygen", "-y", identity)
	recipient, err := key.Output()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(site, "secrets", "site.sops.yaml")
	decrypt := exec.Command("sops", "decrypt", "--output-type", "yaml", path)
	decrypt.Env = append(decrypt.Environ(), "SOPS_AGE_KEY_FILE="+identity)
	plaintext, err := decrypt.Output()
	if err != nil {
		t.Fatal(err)
	}
	encryptSiteValues(t, path, string(plaintext), strings.TrimSpace(string(recipient)))
	sitePath := filepath.Join(site, "site.yaml")
	contents, err := os.ReadFile(sitePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(contents), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, "recoveryRecipient:") {
			filtered = append(filtered, line)
		}
	}
	writeFile(t, sitePath, []byte(strings.Join(filtered, "\n")), 0o600)
	command := homelabCommand(t, "site", "validate", "--site", "topology", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"valid":true`) {
		t.Fatalf("single-recipient topology rejected: %v\n%s", err, output)
	}
}

func TestHomeDeclaresTwoHostAssignedDNSInstances(t *testing.T) {
	unrelatedIdentity, _ := newAgeIdentity(t)
	command := homelabCommand(t, "site", "validate", "--site", "home", "--output", "json")
	privateConfig := t.TempDir()
	command.Env = append(command.Environ(), "SOPS_AGE_KEY_FILE="+unrelatedIdentity, "HOME="+privateConfig, "XDG_CONFIG_HOME="+privateConfig)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("home Site decrypted with unrelated identity: %s", output)
	}
	for _, expected := range []string{`"hosts":3`, `"stackInstances":2`, `"code":"SITE_NETWORK_DECRYPT_FAILED"`, `"primary":"homelab-dns"`, `"secondary":"raspberrypi-dns"`, `"id":"media-laptop-mac","value":"<redacted>"`} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("missing %s: %s", expected, output)
		}
	}
}

func networkTopologyFixture(t *testing.T, overrides map[string]string) (string, string, string) {
	t.Helper()
	dailyIdentity, dailyRecipient := newAgeIdentity(t)
	_, recoveryRecipient := newAgeIdentity(t)
	root := t.TempDir()
	site := filepath.Join(root, "sites", "topology")
	for _, directory := range []string{"hosts", "instances", "secrets"} {
		if err := os.MkdirAll(filepath.Join(site, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(site, "site.yaml"), []byte("apiVersion: homelab.site/v1alpha1\nkind: Site\nmetadata:\n    name: topology\nspec:\n    hosts: [hosts/primary.yaml, hosts/secondary.yaml]\n    stackInstances: [instances/primary.yaml, instances/secondary.yaml]\n    sensitiveValues:\n        document: secrets/site.sops.yaml\n        dailyRecipient: "+dailyRecipient+"\n        recoveryRecipient: "+recoveryRecipient+"\n    dnsCluster:\n        primary: primary-dns\n        secondary: secondary-dns\n    network:\n        subnet: lan-subnet\n        gateway: bell-gateway\n        dhcp:\n            owner: primary-dns\n            reservationStart: reserved-start\n            reservationEnd: reserved-end\n            dynamicStart: dynamic-start\n            dynamicEnd: dynamic-end\n            reservations:\n                - name: printer\n                  address: printer-address\n                  mac: printer-mac\n"), 0o600)
	writeFile(t, filepath.Join(site, "hosts", "primary.yaml"), []byte("apiVersion: homelab.site/v1alpha1\nkind: Host\nmetadata:\n    name: primary\nspec:\n    capabilities: [containers.compose]\n    networkAddress: primary-address\n"), 0o600)
	writeFile(t, filepath.Join(site, "hosts", "secondary.yaml"), []byte("apiVersion: homelab.site/v1alpha1\nkind: Host\nmetadata:\n    name: secondary\nspec:\n    capabilities: []\n    networkAddress: secondary-address\n"), 0o600)
	writeFile(t, filepath.Join(site, "instances", "primary.yaml"), []byte("apiVersion: homelab.site/v1alpha1\nkind: StackInstance\nmetadata:\n    name: primary-dns\nspec:\n    stack: network\n    host: primary\n    runtimeIdentity: primary-dns\n    network:\n        role: primary\n"), 0o600)
	writeFile(t, filepath.Join(site, "instances", "secondary.yaml"), []byte("apiVersion: homelab.site/v1alpha1\nkind: StackInstance\nmetadata:\n    name: secondary-dns\nspec:\n    stack: network\n    host: secondary\n    runtimeIdentity: secondary-dns\n    network:\n        role: secondary\n"), 0o600)
	values := "apiVersion: homelab.site/v1alpha1\nkind: SiteSensitiveValues\nspec:\n    values:\n"
	for _, item := range [][2]string{{"lan-subnet", "192.0.2.0/24"}, {"bell-gateway", "192.0.2.1"}, {"primary-address", "192.0.2.10"}, {"secondary-address", "192.0.2.11"}, {"reserved-start", "192.0.2.40"}, {"reserved-end", "192.0.2.79"}, {"dynamic-start", "192.0.2.100"}, {"dynamic-end", "192.0.2.199"}, {"printer-address", "192.0.2.45"}, {"printer-mac", "02:00:00:00:00:01"}} {
		if replacement, exists := overrides[item[0]]; exists {
			item[1] = replacement
		}
		values += "        - id: " + item[0] + "\n          value: " + item[1] + "\n"
	}
	encryptSiteValues(t, filepath.Join(site, "secrets", "site.sops.yaml"), values, dailyRecipient, recoveryRecipient)
	return root, dailyIdentity, site
}

func TestEncryptedNetworkTopologyValidatesThroughCLI(t *testing.T) {
	root, identity, _ := networkTopologyFixture(t, nil)
	command := homelabCommand(t, "site", "validate", "--site", "topology", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("valid network topology rejected: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"valid":true`) {
		t.Fatalf("validity missing: %s", output)
	}
	if !strings.Contains(string(output), `"networkPlan":`) || !strings.Contains(string(output), `"id":"printer-mac","value":"<redacted>"`) {
		t.Fatalf("redacted network plan missing: %s", output)
	}
	for _, secret := range []string{"192.0.2.", "02:00:00:00:00:01", "ENC["} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("validation exposed sensitive value: %s", output)
		}
	}
}

func TestEncryptedNetworkTopologyRequiresAuthorizedIdentity(t *testing.T) {
	root, _, _ := networkTopologyFixture(t, nil)
	unrelatedIdentity, _ := newAgeIdentity(t)
	command := homelabCommand(t, "site", "validate", "--site", "topology", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+unrelatedIdentity)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), `"code":"SITE_NETWORK_DECRYPT_FAILED"`) {
		t.Fatalf("expected safe decryption failure: %v\n%s", err, output)
	}
	for _, secret := range []string{"192.0.2.", "02:00:00:00:00:01", "ENC["} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("failure exposed sensitive value: %s", output)
		}
	}
}

func TestNetworkTopologyRejectsPlaintextValueInSOPSDocument(t *testing.T) {
	root, identity, site := networkTopologyFixture(t, nil)
	path := filepath.Join(site, "secrets", "site.sops.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(contents), "\n")
	changed := false
	for index, line := range lines {
		if strings.Contains(line, "value: ENC[") {
			lines[index] = "          value: 192.0.2.1"
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("fixture contains no encrypted value")
	}
	writeFile(t, path, []byte(strings.Join(lines, "\n")), 0o600)
	command := homelabCommand(t, "site", "validate", "--site", "topology", "--output", "json")
	command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), `"code":"SITE_NETWORK_VALUE_UNENCRYPTED"`) || strings.Contains(string(output), "192.0.2.") {
		t.Fatalf("plaintext value was not rejected safely: %v\n%s", err, output)
	}
}

func TestEncryptedNetworkTopologyRejectsUnsafePlansWithoutDisclosingValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		mutate func(string)
		code   string
	}{
		{"duplicate static address", map[string]string{"secondary-address": "192.0.2.10"}, nil, "SITE_NETWORK_ADDRESS_DUPLICATE"},
		{"out of subnet", map[string]string{"primary-address": "198.51.100.10"}, nil, "SITE_NETWORK_ADDRESS_INVALID"},
		{"dynamic allocation overlaps static", map[string]string{"dynamic-start": "192.0.2.10"}, nil, "SITE_NETWORK_STATIC_OVERLAP"},
		{"reservation outside reserved band", map[string]string{"printer-address": "192.0.2.90"}, nil, "SITE_NETWORK_RESERVATION_INVALID"},
		{"invalid reservation MAC", map[string]string{"printer-mac": "not-a-mac"}, nil, "SITE_NETWORK_RESERVATION_INVALID"},
		{"missing Host", nil, func(site string) {
			path := filepath.Join(site, "instances", "secondary.yaml")
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, []byte(strings.Replace(string(contents), "host: secondary", "host: missing", 1)), 0o600)
		}, "SITE_HOST_REFERENCE_MISSING"},
		{"inconsistent DHCP owner", nil, func(site string) {
			path := filepath.Join(site, "site.yaml")
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, []byte(strings.Replace(string(contents), "owner: primary-dns", "owner: secondary-dns", 1)), 0o600)
		}, "SITE_NETWORK_DHCP_OWNER_INVALID"},
	}
	for _, check := range tests {
		t.Run(check.name, func(t *testing.T) {
			root, identity, site := networkTopologyFixture(t, check.values)
			if check.mutate != nil {
				check.mutate(site)
			}
			command := homelabCommand(t, "site", "validate", "--site", "topology", "--output", "json")
			command.Env = append(command.Environ(), "HOMELAB_REPOSITORY_ROOT="+root, "SOPS_AGE_KEY_FILE="+identity)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), `"code":"`+check.code+`"`) {
				t.Fatalf("want %s: %v\n%s", check.code, err, output)
			}
			for _, sensitive := range []string{"192.0.2.", "198.51.100.", "not-a-mac", "ENC["} {
				if strings.Contains(string(output), sensitive) {
					t.Fatalf("validation exposed sensitive value: %s", output)
				}
			}
		})
	}
}
