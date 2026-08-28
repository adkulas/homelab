package acceptance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInitWizardExplainsAndCollectsSupportedOpenVPNChoices(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	configuration := string(uninitializedConfiguration(t))
	configuration = strings.Replace(configuration, "timezone: America/Toronto", `timezone: ""`, 1)
	configuration = strings.Replace(configuration, "runtimeUID: 1000", "runtimeUID: 0", 1)
	configuration = strings.Replace(configuration, "runtimeGID: 1000", "runtimeGID: 0", 1)
	configuration = strings.ReplaceAll(configuration, "hardwareTranscoding: auto", `hardwareTranscoding: ""`)
	writeFile(t, configPath, []byte(configuration), 0o640)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'nordvpn: ENC[interactive-ciphertext]\\n'\n"), 0o700)

	uidInput, selectedUID := "", os.Getuid()
	if selectedUID <= 0 {
		uidInput, selectedUID = "1234", 1234
	}
	gidInput, selectedGID := "", os.Getgid()
	if selectedGID <= 0 {
		gidInput, selectedGID = "2345", 2345
	}

	command := exec.Command("go", "run", "./cmd/media-stack", "init", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.Stdin = strings.NewReader(strings.Join([]string{
		uidInput,           // suggested operator UID when non-root
		gidInput,           // suggested operator GID when non-root
		"y",                // visible identity confirmation
		"America/Toronto",  // timezone
		"Canada",           // country
		"P2P",              // optional supported category
		"",                 // default OpenVPN UDP
		"",                 // default 480h catalogue update
		"",                 // default automatic hardware detection
		"age1interactive",  // age recipient
		"service-user",     // Nord manual-setup username
		"service-password", // Nord manual-setup password
		"fixture-profilarr-api-key-32-characters", // Profilarr API key
		"household",                    // Jellyfin administrator username
		"fixture-jellyfin-password",    // Jellyfin administrator password
		"household",                    // qBittorrent Web UI username
		"fixture-qbittorrent-password", // qBittorrent Web UI password
	}, "\n") + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("interactive media-stack init failed: %v\n%s", err, output)
	}
	humanOutput := string(output)
	for _, guidance := range []string{
		fmt.Sprintf("Runtime UID for intended operator [%d]", os.Getuid()),
		fmt.Sprintf("Runtime GID for intended operator [%d]", os.Getgid()),
		fmt.Sprintf("Confirm runtime identity %d:%d [y/N]", selectedUID, selectedGID),
		"Nord Account manual-setup area",
		"not your Nord account email/password",
		"Profilarr API key",
		"Jellyfin administrator username",
		"Jellyfin administrator password",
		"qBittorrent Web UI username",
		"qBittorrent Web UI password",
		"OpenVPN protocol (udp or tcp) [udp]",
		"Hardware transcoding (auto or disabled) [auto]",
	} {
		if !strings.Contains(humanOutput, guidance) {
			t.Errorf("wizard output is missing %q:\n%s", guidance, humanOutput)
		}
	}
	if strings.Contains(strings.ToLower(humanOutput), "wireguard") {
		t.Errorf("first-milestone wizard offered WireGuard:\n%s", humanOutput)
	}
	for _, secret := range []string{"service-user", "service-password", "fixture-jellyfin-password", "fixture-qbittorrent-password"} {
		if strings.Contains(humanOutput, secret) {
			t.Errorf("wizard output exposed secret %q:\n%s", secret, humanOutput)
		}
	}

	var declared struct {
		Spec struct {
			Environments map[string]struct {
				HardwareTranscoding string `yaml:"hardwareTranscoding"`
			} `yaml:"environments"`
			Defaults struct {
				RuntimeUID int `yaml:"runtimeUID"`
				RuntimeGID int `yaml:"runtimeGID"`
			} `yaml:"defaults"`
			Acquisition struct {
				VPN struct {
					OpenVPNProtocol string `yaml:"openvpnProtocol"`
				} `yaml:"vpn"`
			} `yaml:"acquisition"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(readFile(t, configPath), &declared); err != nil {
		t.Fatal(err)
	}
	if declared.Spec.Defaults.RuntimeUID != selectedUID || declared.Spec.Defaults.RuntimeGID != selectedGID {
		t.Errorf("suggested operator identity was not persisted: %#v", declared.Spec.Defaults)
	}
	if declared.Spec.Acquisition.VPN.OpenVPNProtocol != "udp" {
		t.Errorf("default OpenVPN protocol = %q, want udp", declared.Spec.Acquisition.VPN.OpenVPNProtocol)
	}
	if got := declared.Spec.Environments["staging"].HardwareTranscoding; got != "auto" {
		t.Errorf("default hardwareTranscoding = %q, want auto", got)
	}
}
