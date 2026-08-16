package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInitCreatesSelectedEnvironmentDeclaredConfigurationAndEncryptedSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyUninitializedConfig(t, configPath, 0o640)
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, []byte(`runtimeUID: 1234
runtimeGID: 2345
timezone: America/Toronto
country: Canada
serverCategory: P2P
openvpnProtocol: udp
catalogueUpdateInterval: 480h
ageRecipient: age1example
serviceUsername: nord-service-user
servicePassword: nord-service-password
`), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' 'nordvpn: ENC[AES256_GCM,data:encrypted]' 'sops:' '  version: 3.9.0'\n"), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "init",
		"--environment", "staging",
		"--config", configPath,
		"--non-interactive",
		"--answers", answersPath,
	)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack init failed: %v\n%s", err, output)
	}
	for _, secret := range []string{"nord-service-user", "nord-service-password"} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("command output exposed secret %q:\n%s", secret, output)
		}
	}

	var declared struct {
		Spec struct {
			Defaults struct {
				Timezone   string `yaml:"timezone"`
				RuntimeUID int    `yaml:"runtimeUID"`
				RuntimeGID int    `yaml:"runtimeGID"`
			} `yaml:"defaults"`
			Acquisition struct {
				VPN struct {
					Protocol        string `yaml:"protocol"`
					OpenVPNProtocol string `yaml:"openvpnProtocol"`
					Server          struct {
						Countries  []string `yaml:"countries"`
						Categories []string `yaml:"categories"`
					} `yaml:"server"`
					CatalogueUpdateInterval string `yaml:"catalogueUpdateInterval"`
				} `yaml:"vpn"`
			} `yaml:"acquisition"`
		} `yaml:"spec"`
	}
	configuration := readFile(t, configPath)
	if err := yaml.Unmarshal(configuration, &declared); err != nil {
		t.Fatalf("decode initialized Declared Configuration: %v\n%s", err, configuration)
	}
	if declared.Spec.Defaults.RuntimeUID != 1234 || declared.Spec.Defaults.RuntimeGID != 2345 || declared.Spec.Defaults.Timezone != "America/Toronto" {
		t.Fatalf("runtime defaults = %#v", declared.Spec.Defaults)
	}
	vpn := declared.Spec.Acquisition.VPN
	if vpn.Protocol != "openvpn" || vpn.OpenVPNProtocol != "udp" || vpn.CatalogueUpdateInterval != "480h" {
		t.Fatalf("VPN configuration = %#v", vpn)
	}
	if len(vpn.Server.Countries) != 1 || vpn.Server.Countries[0] != "Canada" || len(vpn.Server.Categories) != 1 || vpn.Server.Categories[0] != "P2P" {
		t.Fatalf("server selection = %#v", vpn.Server)
	}

	secretPath := filepath.Join(temporary, "secrets", "staging.sops.yaml")
	secretDocument := string(readFile(t, secretPath))
	if !strings.Contains(secretDocument, "ENC[") || strings.Contains(secretDocument, "nord-service-") {
		t.Fatalf("secret document is not encrypted or exposed plaintext:\n%s", secretDocument)
	}
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secret document mode = %#o, want 0600", got)
	}
}

func uninitializedConfiguration(t *testing.T) []byte {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), &document); err != nil {
		t.Fatal(err)
	}
	spec, ok := document["spec"].(map[string]any)
	if !ok {
		t.Fatal("Declared Configuration has no spec mapping")
	}
	delete(spec, "acquisition")
	contents, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func copyUninitializedConfig(t *testing.T, destination string, mode os.FileMode) {
	t.Helper()
	writeFile(t, destination, uninitializedConfiguration(t), mode)
}

func copyFile(t *testing.T, source, destination string, mode os.FileMode) {
	t.Helper()
	writeFile(t, destination, readFile(t, source), mode)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func writeFile(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}
