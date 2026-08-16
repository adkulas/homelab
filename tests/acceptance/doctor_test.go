package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsStableMachineReadablePrerequisiteFailures(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		code     string
	}{
		{"missing TUN", "tun", "PREFLIGHT_TUN_UNAVAILABLE"},
		{"unavailable NET_ADMIN", "net-admin", "PREFLIGHT_NET_ADMIN_UNAVAILABLE"},
		{"unsupported server filters", "filters", "PREFLIGHT_VPN_FILTER_UNSUPPORTED"},
		{"secret decryption failure", "secret", "SECRET_DECRYPT_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configPath, binDirectory := doctorFixture(t)
			command := exec.Command("go", "run", "./cmd/media-stack", "doctor",
				"--environment", "staging", "--config", configPath, "--output", "json")
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(),
				"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DOCTOR_SCENARIO="+test.scenario,
			)
			output, err := command.Output()
			if err == nil {
				t.Fatal("doctor unexpectedly passed")
			}
			var report struct {
				SchemaVersion string `json:"schemaVersion"`
				Environment   string `json:"environment"`
				Diagnostics   []struct {
					Code   string `json:"code"`
					Status string `json:"status"`
					Remedy string `json:"remedy"`
				} `json:"diagnostics"`
			}
			if decodeErr := json.Unmarshal(output, &report); decodeErr != nil {
				t.Fatalf("doctor did not emit one JSON document: %v\n%s", decodeErr, output)
			}
			if report.SchemaVersion != "homelab.media-stack/doctor/v1alpha1" || report.Environment != "staging" {
				t.Fatalf("report identity = %#v", report)
			}
			found := false
			for _, diagnostic := range report.Diagnostics {
				if diagnostic.Code == test.code {
					found = diagnostic.Status == "fail" && diagnostic.Remedy != ""
				}
			}
			if !found {
				t.Fatalf("missing actionable %s diagnostic: %s", test.code, output)
			}
			if strings.Contains(string(output), "doctor-secret-value") {
				t.Fatalf("doctor exposed a decrypted secret: %s", output)
			}
		})
	}
}

func TestDoctorUsesAValidLinuxInterfaceNameForTheNetAdminProbe(t *testing.T) {
	configPath, binDirectory := doctorFixture(t)
	command := exec.Command("go", "run", "./cmd/media-stack", "doctor",
		"--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCTOR_SCENARIO=ifname",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("doctor used an invalid Linux interface name: %v\n%s", err, output)
	}
}

func doctorFixture(t *testing.T) (string, string) {
	t.Helper()
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	configuration := readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml"))
	if !strings.Contains(string(configuration), "\n  acquisition:") && !strings.Contains(string(configuration), "\n    acquisition:") {
		configuration = append(configuration, []byte(`  acquisition:
    vpn:
      provider: nordvpn
      protocol: openvpn
      openvpnProtocol: udp
      server:
        countries: [Canada]
        categories: [P2P]
      catalogueUpdateInterval: 480h
`)...)
	}
	writeFile(t, configPath, configuration, 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	docker := `#!/bin/sh
case "$*" in
  *"--device /dev/net/tun"*) [ "$DOCTOR_SCENARIO" = tun ] && exit 1; exit 0 ;;
  *"--cap-add NET_ADMIN"*)
    [ "$DOCTOR_SCENARIO" = net-admin ] && exit 1
    if [ "$DOCTOR_SCENARIO" = ifname ]; then
      interface=${*#*ip link add }
      interface=${interface%% *}
      [ ${#interface} -le 15 ] || exit 1
    fi
    exit 0 ;;
  *"format-servers -nordvpn"*) [ "$DOCTOR_SCENARIO" = filters ] && exit 1; echo canada-p2p; exit 0 ;;
esac
exit 0
`
	sops := `#!/bin/sh
if [ "$1" = decrypt ]; then
  [ "$DOCTOR_SCENARIO" = secret ] && exit 1
  echo 'nordvpn: doctor-secret-value'
fi
exit 0
`
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(docker), 0o700)
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(sops), 0o700)
	writeFile(t, filepath.Join(binDirectory, "age"), []byte("#!/bin/sh\nexit 0\n"), 0o700)
	return configPath, binDirectory
}
