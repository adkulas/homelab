package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDoctorProbesTheApplicationStorageMountsAsTheDeclaredRuntimeIdentity(t *testing.T) {
	configPath, binDirectory := doctorFixture(t)
	dockerLog := filepath.Join(t.TempDir(), "docker.log")
	command := exec.Command("go", "run", "./cmd/media-stack", "doctor",
		"--environment", "staging", "--config", configPath, "--output", "json")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCTOR_DOCKER_LOG="+dockerLog,
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("doctor rejected capable storage: %v\n%s", err, output)
	}

	var report struct {
		Diagnostics []struct {
			Code   string `json:"code"`
			Status string `json:"status"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode doctor report: %v\n%s", err, output)
	}
	passed := map[string]bool{}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Status == "pass" {
			passed[diagnostic.Code] = true
		}
	}
	for _, code := range []string{
		"PREFLIGHT_STORAGE_RUNTIME_IDENTITY",
		"PREFLIGHT_STORAGE_PERMISSIONS",
		"PREFLIGHT_STORAGE_SAME_DEVICE",
		"PREFLIGHT_STORAGE_HARDLINK",
		"PREFLIGHT_STORAGE_INODE_IDENTITY",
		"PREFLIGHT_STORAGE_ATOMIC_RENAME",
		"PREFLIGHT_STORAGE_FILESYSTEM_EVENTS",
		"PREFLIGHT_STORAGE_CLEANUP",
	} {
		if !passed[code] {
			t.Errorf("doctor did not report a passing %s diagnostic: %s", code, output)
		}
	}

	log := string(readFile(t, dockerLog))
	identity := "--user " + strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	want := []string{
		identity + " --mount type=bind,source=/srv/media/staging/torrents,target=/data/torrents",
		identity + " --mount type=bind,source=/srv/media/staging,target=/data",
		"__storage-probe --source /data/torrents/movies --destination /data/media/movies",
		"__storage-probe --source /data/torrents/series --destination /data/media/series",
	}
	for _, fragment := range want {
		if !strings.Contains(log, fragment) {
			t.Errorf("Docker calls do not contain %q:\n%s", fragment, log)
		}
	}
	if count := strings.Count(log, "__storage-probe"); count != 3 {
		t.Errorf("storage probe count = %d, want 3:\n%s", count, log)
	}
}

func TestStorageProbeExercisesFilesystemCapabilitiesAndRemovesOnlyItsArtifacts(t *testing.T) {
	temporary := t.TempDir()
	source := filepath.Join(temporary, "torrents", "movies")
	destination := filepath.Join(temporary, "media", "movies")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(source, "existing-media.mkv")
	writeFile(t, sentinel, []byte("preserve me"), 0o640)

	command := exec.Command("go", "run", "./cmd/media-stack", "__storage-probe",
		"--source", source,
		"--destination", destination,
		"--uid", strconv.Itoa(os.Getuid()),
		"--gid", strconv.Itoa(os.Getgid()),
	)
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("storage probe failed: %v\n%s", err, output)
	}
	var report struct {
		Checks []struct {
			Name        string `json:"name"`
			Passed      bool   `json:"passed"`
			SourceInode uint64 `json:"sourceInode,omitempty"`
			TargetInode uint64 `json:"targetInode,omitempty"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode storage probe report: %v\n%s", err, output)
	}
	checks := map[string]bool{}
	for _, check := range report.Checks {
		checks[check.Name] = check.Passed
		if check.Name == "inode_identity" &&
			(check.SourceInode == 0 || check.SourceInode != check.TargetInode) {
			t.Errorf("hardlink inode evidence = %d and %d", check.SourceInode, check.TargetInode)
		}
	}
	for _, name := range []string{
		"runtime_identity", "permissions", "same_device", "hardlink",
		"inode_identity", "atomic_rename", "filesystem_events", "cleanup",
	} {
		if !checks[name] {
			t.Errorf("storage probe did not pass %s: %s", name, output)
		}
	}
	if got := string(readFile(t, sentinel)); got != "preserve me" {
		t.Errorf("sentinel contents = %q", got)
	}
	sourceEntries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceEntries) != 1 || sourceEntries[0].Name() != filepath.Base(sentinel) {
		t.Errorf("source contains unexpected artifacts: %#v", sourceEntries)
	}
	destinationEntries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(destinationEntries) != 0 {
		t.Errorf("destination contains unexpected artifacts: %#v", destinationEntries)
	}
}

func TestDoctorReportsActionableStorageCapabilityFailures(t *testing.T) {
	tests := []struct {
		capability string
		code       string
	}{
		{"runtime_identity", "PREFLIGHT_STORAGE_RUNTIME_IDENTITY"},
		{"permissions", "PREFLIGHT_STORAGE_PERMISSIONS"},
		{"same_device", "PREFLIGHT_STORAGE_SAME_DEVICE"},
		{"hardlink", "PREFLIGHT_STORAGE_HARDLINK"},
		{"inode_identity", "PREFLIGHT_STORAGE_INODE_IDENTITY"},
		{"atomic_rename", "PREFLIGHT_STORAGE_ATOMIC_RENAME"},
		{"filesystem_events", "PREFLIGHT_STORAGE_FILESYSTEM_EVENTS"},
		{"cleanup", "PREFLIGHT_STORAGE_CLEANUP"},
	}
	for _, test := range tests {
		t.Run(test.capability, func(t *testing.T) {
			configPath, binDirectory := doctorFixture(t)
			command := exec.Command("go", "run", "./cmd/media-stack", "doctor",
				"--environment", "staging", "--config", configPath, "--output", "json")
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(),
				"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DOCTOR_STORAGE_FAILURE="+test.capability,
			)
			output, err := command.Output()
			if err == nil {
				t.Fatal("doctor unexpectedly accepted unsupported storage")
			}
			var report struct {
				Diagnostics []struct {
					Code        string `json:"code"`
					Status      string `json:"status"`
					Explanation string `json:"explanation"`
					Remedy      string `json:"remedy"`
					Retryable   bool   `json:"retryable"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal(output, &report); err != nil {
				t.Fatalf("decode doctor report: %v\n%s", err, output)
			}
			found := false
			for _, diagnostic := range report.Diagnostics {
				if diagnostic.Code == test.code && diagnostic.Status == "fail" {
					found = diagnostic.Explanation != "" && diagnostic.Remedy != "" && diagnostic.Retryable
				}
			}
			if !found {
				t.Fatalf("missing actionable %s failure: %s", test.code, output)
			}
		})
	}
}

func TestDoctorRejectsAnIncompleteStorageProbeReport(t *testing.T) {
	configPath, binDirectory := doctorFixture(t)
	command := exec.Command("go", "run", "./cmd/media-stack", "doctor",
		"--environment", "staging", "--config", configPath, "--output", "json")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCTOR_STORAGE_FAILURE=incomplete",
	)
	output, err := command.Output()
	if err == nil {
		t.Fatal("doctor accepted an incomplete storage probe report")
	}
	if !strings.Contains(string(output), `"code":"PREFLIGHT_STORAGE_PROBE_INVALID"`) ||
		!strings.Contains(string(output), `"status":"fail"`) {
		t.Fatalf("missing invalid-probe diagnostic: %s", output)
	}
}

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
[ -n "$DOCTOR_DOCKER_LOG" ] && printf '%s\n' "$*" >> "$DOCTOR_DOCKER_LOG"
case "$*" in
	*"__storage-probe"*)
		if [ "$DOCTOR_STORAGE_FAILURE" = incomplete ]; then
			printf '%s\n' '{"checks":[]}'
			exit 0
		fi
		runtime_identity=true permissions=true same_device=true hardlink=true
		inode_identity=true atomic_rename=true filesystem_events=true cleanup=true
		case "$DOCTOR_STORAGE_FAILURE" in
			runtime_identity) runtime_identity=false ;;
			permissions) permissions=false ;;
			same_device) same_device=false ;;
			hardlink) hardlink=false ;;
			inode_identity) inode_identity=false ;;
			atomic_rename) atomic_rename=false ;;
			filesystem_events) filesystem_events=false ;;
			cleanup) cleanup=false ;;
		esac
		printf '{"checks":[{"name":"runtime_identity","passed":%s},{"name":"permissions","passed":%s},{"name":"same_device","passed":%s},{"name":"hardlink","passed":%s},{"name":"inode_identity","passed":%s},{"name":"atomic_rename","passed":%s},{"name":"filesystem_events","passed":%s},{"name":"cleanup","passed":%s,"explanation":"fixture rejects capability"}]}\n' \
			"$runtime_identity" "$permissions" "$same_device" "$hardlink" "$inode_identity" "$atomic_rename" "$filesystem_events" "$cleanup"
		exit 0 ;;
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
