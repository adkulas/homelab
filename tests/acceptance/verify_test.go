package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyProvesVPNEgressFailClosedAndRecovery(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	dockerLog := filepath.Join(temporary, "docker.log")
	probeCount := filepath.Join(temporary, "probe-count")
	writeFile(t, filepath.Join(binDirectory, "curl"), []byte("#!/bin/sh\nprintf '198.51.100.10\\n'\n"), 0o700)
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\nprintf 'nordvpn:\n  openvpn:\n    serviceUsername: fixture-user\n    servicePassword: fixture-password\nprofilarr:\n  apiKey: fixture-profilarr-api-key-32-characters\njellyfin:\n  username: household\n  password: fixture-jellyfin-password\nqbittorrent:\n  username: household\n  password: fixture-qbittorrent-password\n'\n"), 0o700)
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$VERIFY_DOCKER_LOG"
cat >/dev/null
case "$*" in
  "run --rm --device /dev/net/tun --entrypoint /bin/sh "*) exit 0 ;;
  "compose -f - ps --format json gluetun") printf '{"Health":"healthy","State":"running"}\n'; exit 0 ;;
  "compose -f - logs --no-color gluetun") exit 0 ;;
  "compose -f - exec -T qbittorrent curl --ipv4 --fail --silent --show-error --max-time 10 "*)
    count=0
    [ -f "$VERIFY_PROBE_COUNT" ] && count=$(cat "$VERIFY_PROBE_COUNT")
    count=$((count + 1))
    printf '%s' "$count" > "$VERIFY_PROBE_COUNT"
    [ "$count" -eq 2 ] && exit 28
    printf '198.51.100.20\n'
    exit 0 ;;
  "compose -f - stop gluetun") exit 0 ;;
  "compose -f - start gluetun") exit 0 ;;
  "compose -f - up -d --force-recreate qbittorrent") exit 0 ;;
  "run --rm --no-healthcheck -i --network media-staging_application --entrypoint /bin/sh "*) exit 0 ;;
  *) exit 99 ;;
esac
`), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "verify",
		"--environment", "staging", "--config", configPath, "--suite", "full", "--output", "json")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERIFY_DOCKER_LOG="+dockerLog,
		"VERIFY_PROBE_COUNT="+probeCount,
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("media-stack verify failed: %v\n%s", err, output)
	}

	var report struct {
		SchemaVersion string `json:"schemaVersion"`
		Environment   string `json:"environment"`
		Suite         string `json:"suite"`
		Diagnostics   []struct {
			Code   string `json:"code"`
			Status string `json:"status"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode verify report: %v\n%s", err, output)
	}
	if report.SchemaVersion != "homelab.media-stack/verify/v1alpha1" || report.Environment != "staging" || report.Suite != "full" {
		t.Errorf("verify report identity = %#v", report)
	}
	passed := map[string]bool{}
	for _, diagnostic := range report.Diagnostics {
		passed[diagnostic.Code] = diagnostic.Status == "pass"
	}
	for _, code := range []string{
		"VERIFY_TUN_AVAILABLE",
		"VERIFY_TUNNEL_HEALTHY",
		"VERIFY_VPN_EGRESS",
		"VERIFY_FAIL_CLOSED",
		"VERIFY_TUNNEL_RECOVERED",
		"VERIFY_QBITTORRENT_AUTH_PERSISTED",
	} {
		if !passed[code] {
			t.Errorf("verify did not report passing %s: %s", code, output)
		}
	}

	log := string(readFile(t, dockerLog))
	wantOrder := []string{
		"run --rm --device /dev/net/tun --entrypoint /bin/sh",
		"ps --format json gluetun",
		"exec -T qbittorrent curl",
		"stop gluetun",
		"exec -T qbittorrent curl",
		"start gluetun",
		"ps --format json gluetun",
		"up -d --force-recreate qbittorrent",
		"exec -T qbittorrent curl",
		"run --rm --no-healthcheck -i --network media-staging_application",
	}
	position := 0
	for _, fragment := range wantOrder {
		next := strings.Index(log[position:], fragment)
		if next < 0 {
			t.Fatalf("Docker calls missing ordered %q after byte %d:\n%s", fragment, position, log)
		}
		position += next + len(fragment)
	}
}

func TestVerifyReportsDistinctMachineReadableFailures(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		code     string
	}{
		{"missing TUN", "tun", "VERIFY_TUN_UNAVAILABLE"},
		{"invalid service credentials", "credentials", "VERIFY_INVALID_SERVICE_CREDENTIALS"},
		{"empty server selection", "selection", "VERIFY_SERVER_SELECTION_EMPTY"},
		{"unhealthy tunnel", "unhealthy", "VERIFY_TUNNEL_UNHEALTHY"},
		{"leaked host egress", "leak", "VERIFY_EGRESS_LEAKED"},
		{"failed recovery", "recovery", "VERIFY_RECOVERY_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temporary := t.TempDir()
			configPath := filepath.Join(temporary, "media-stack.yaml")
			configuration := readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml"))
			if test.scenario == "selection" {
				configuration = []byte(strings.Replace(string(configuration), "countries:\n                    - Canada", "countries: []", 1))
			}
			writeFile(t, configPath, configuration, 0o600)
			binDirectory := filepath.Join(temporary, "bin")
			if err := os.Mkdir(binDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(binDirectory, "curl"), []byte("#!/bin/sh\nprintf '198.51.100.10\\n'\n"), 0o700)
			writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
cat >/dev/null
case "$*" in
  "run --rm --device /dev/net/tun --entrypoint /bin/sh "*)
    [ "$VERIFY_SCENARIO" = tun ] && exit 1
    exit 0 ;;
  "compose -f - ps --format json gluetun")
    case "$VERIFY_SCENARIO" in credentials|unhealthy) printf '{"Health":"unhealthy","State":"running"}\n' ;; *) printf '{"Health":"healthy","State":"running"}\n' ;; esac
    exit 0 ;;
  "compose -f - logs --no-color gluetun")
    [ "$VERIFY_SCENARIO" = credentials ] && printf 'AUTH_FAILED: authentication failed\n'
    exit 0 ;;
  "compose -f - exec -T qbittorrent curl --ipv4 --fail --silent --show-error --max-time 10 "*)
    count=0
    [ -f "$VERIFY_PROBE_COUNT" ] && count=$(cat "$VERIFY_PROBE_COUNT")
    count=$((count + 1))
    printf '%s' "$count" > "$VERIFY_PROBE_COUNT"
    if [ "$VERIFY_SCENARIO" = leak ] && [ "$count" -eq 2 ]; then printf '198.51.100.10\n'; exit 0; fi
    [ "$count" -eq 2 ] && exit 28
    printf '198.51.100.20\n'
    exit 0 ;;
  "compose -f - stop gluetun") exit 0 ;;
  "compose -f - start gluetun") [ "$VERIFY_SCENARIO" = recovery ] && exit 1; exit 0 ;;
  "compose -f - up -d --force-recreate qbittorrent") exit 0 ;;
  *) exit 99 ;;
 esac
`), 0o700)

			command := exec.Command("go", "run", "./cmd/media-stack", "verify",
				"--environment", "staging", "--config", configPath, "--suite", "full", "--output", "json")
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(),
				"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
				"VERIFY_SCENARIO="+test.scenario,
				"VERIFY_PROBE_COUNT="+filepath.Join(temporary, "probe-count"),
			)
			output, err := command.Output()
			if err == nil {
				t.Fatalf("verify accepted %s: %s", test.name, output)
			}
			if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
				t.Fatalf("verify %s exit = %v, want operational exit 1", test.name, err)
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
				t.Fatalf("decode verify failure: %v\n%s", err, output)
			}
			found := false
			for _, diagnostic := range report.Diagnostics {
				if diagnostic.Code == test.code && diagnostic.Status == "fail" {
					found = diagnostic.Explanation != "" && diagnostic.Remedy != "" && diagnostic.Retryable
				}
			}
			if !found {
				t.Fatalf("missing actionable %s diagnostic: %s", test.code, output)
			}
		})
	}
}

func TestVerifyRejectsDisruptiveFullSuiteForProduction(t *testing.T) {
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "media-stack")
	build := exec.Command("go", "build", "-o", binary, "./cmd/media-stack")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build media-stack: %v\n%s", err, output)
	}
	command := exec.Command(binary, "verify", "--environment", "production", "--suite", "full")
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("verify accepted disruptive Production suite: %s", output)
	}
	if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 64 {
		t.Fatalf("Production full-suite exit = %v, want usage exit 64", err)
	}
	if !strings.Contains(string(output), "full verification is disruptive and requires the Staging Environment") {
		t.Fatalf("Production rejection is not actionable: %s", output)
	}
}
