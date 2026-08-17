package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyStartsQBittorrentOnlyAfterHealthyGluetunWithRuntimeSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
cat <<'EOF'
nordvpn:
  openvpn:
    serviceUsername: apply-service-user
    servicePassword: apply-service-password
EOF
`), 0o700)
	dockerLog := filepath.Join(temporary, "docker-arguments")
	composeCapture := filepath.Join(temporary, "compose.yaml")
	healthCount := filepath.Join(temporary, "health-count")
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
	printf '%s\n' "$*" >> "$APPLY_DOCKER_LOG"
	cat > "$APPLY_COMPOSE_CAPTURE"
	case "$*" in
	  "compose -f - up -d gluetun") exit 0 ;;
	  "compose -f - up -d qbittorrent") exit 0 ;;
	  "compose -f - ps --format json gluetun")
	    count=0
	    [ -f "$APPLY_HEALTH_COUNT" ] && count=$(cat "$APPLY_HEALTH_COUNT")
	    count=$((count + 1))
	    printf '%s' "$count" > "$APPLY_HEALTH_COUNT"
	    [ "$count" -eq 1 ] && printf '{"Health":"unhealthy","State":"running"}\n' || printf '{"Health":"healthy","State":"running"}\n'
	    exit 0 ;;
	  *) exit 99 ;;
	esac
`), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+filepath.Join(temporary, "runtime"),
		"APPLY_DOCKER_LOG="+dockerLog,
		"APPLY_COMPOSE_CAPTURE="+composeCapture,
		"APPLY_HEALTH_COUNT="+healthCount,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack apply failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Started qBittorrent behind healthy Gluetun for the staging Environment.") {
		t.Errorf("apply output = %q, want completed VPN-confined qBittorrent phase", output)
	}

	wantDocker := "compose -f - up -d gluetun\ncompose -f - ps --format json gluetun\ncompose -f - ps --format json gluetun\ncompose -f - up -d qbittorrent"
	if got := strings.TrimSpace(string(readFile(t, dockerLog))); got != wantDocker {
		t.Errorf("Docker invocation = %q", got)
	}
	rendered := string(readFile(t, composeCapture))
	for _, secret := range []string{"apply-service-user", "apply-service-password"} {
		if strings.Contains(rendered, secret) || strings.Contains(string(output), secret) {
			t.Errorf("apply exposed secret %q\noutput: %s\nCompose: %s", secret, output, rendered)
		}
	}

	runtimeDirectory := filepath.Join(temporary, "runtime", "media-stack", "media-staging")
	if info, statErr := os.Stat(runtimeDirectory); statErr != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime secret directory mode = %v, %v; want 0700", info, statErr)
	}
	for name, want := range map[string]string{
		"openvpn_user":     "apply-service-user",
		"openvpn_password": "apply-service-password",
	} {
		path := filepath.Join(runtimeDirectory, name)
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("runtime secret %s mode = %v, %v; want 0600", name, info, statErr)
		}
		if got := strings.TrimSpace(string(readFile(t, path))); got != want {
			t.Errorf("runtime secret %s = %q, want selected credential", name, got)
		}
	}
}

func TestApplyRedactsCredentialsFromUnhealthyGluetunFailure(t *testing.T) {
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "media-stack")
	build := exec.Command("go", "build", "-o", binary, "./cmd/media-stack")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build media-stack: %v\n%s", err, output)
	}
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
printf 'nordvpn:\n  openvpn:\n    serviceUsername: failure-service-user\n    servicePassword: failure-service-password\n'
`), 0o700)
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
cat >/dev/null
printf 'authentication failed for failure-service-user with failure-service-password\n' >&2
exit 1
`), 0o700)

	command := exec.Command(binary, "apply", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+filepath.Join(temporary, "runtime"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("apply accepted unhealthy Gluetun: %s", output)
	}
	if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
		t.Errorf("unhealthy Gluetun exit = %v, want operational exit 1", err)
	}
	for _, secret := range []string{"failure-service-user", "failure-service-password"} {
		if strings.Contains(string(output), secret) {
			t.Errorf("apply failure exposed secret %q: %s", secret, output)
		}
	}
	if !strings.Contains(string(output), "start healthy Gluetun") || !strings.Contains(string(output), "[REDACTED]") {
		t.Errorf("apply failure is not actionable and redacted: %s", output)
	}
}
