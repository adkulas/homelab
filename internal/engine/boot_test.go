package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

func TestBootRegistrationAndRecovery(t *testing.T) {
	t.Setenv("SYSTEMD_OFFLINE", "1")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	root := t.TempDir()
	bin := filepath.Join(root, "tools")
	for name, script := range map[string]string{
		"loginctl":  "exit 0",
		"systemctl": `echo "$*" >> "$BOOT_MANAGER_LOG"`,
		"sops": `cat <<'DOC'
nordvpn: {openvpn: {serviceUsername: user, servicePassword: fixture-password}}
jellyfin: {username: user, password: example}
qbittorrent: {username: user, password: example}
profilarr: {apiKey: '0123456789abcdef0123456789abcdef'}
DOC`,
		"docker": `cat >/dev/null
echo "$*" >> "$BOOT_DOCKER_LOG"
case "$*" in
  *"start gluetun") test "$(cat "$BOOT_VPN_FILE")" = fixture-password ;;
  *"ps --format json gluetun") echo "{\"State\":\"running\",\"Health\":\"$BOOT_HEALTH\"}" ;;
  *"start qbittorrent") exit 0 ;;
  *) exit 1 ;;
esac`,
	} {
		if err := writeFileAtomic(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	t.Setenv("BOOT_MANAGER_LOG", filepath.Join(root, "systemctl.log"))
	log := filepath.Join(root, "docker.log")
	t.Setenv("BOOT_DOCKER_LOG", log)
	declared, err := config.Load("../../tests/fixtures/media-stack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env := declared.Spec.Environments["staging"]
	env.ProjectName = "boot-" + checksum([]byte(root))[:12]
	env.BackupRoot = filepath.Join(root, "backups")
	declared.Spec.Environments["staging"] = env
	m := bootManager{filepath.Join(root, "config"), filepath.Join(root, "units"), true}
	ctx := context.Background()
	req := PlanRequest{environment: "staging", configPath: filepath.Join(root, "config.yml")}
	for range 2 {
		if err = m.install(ctx, req, declared, Plan{compose: []byte("name: boot")}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(m.directory(env.ProjectName), "recovery.json")
	t.Setenv("BOOT_VPN_FILE", filepath.Join(environmentRuntimeSecretDirectory(env), "openvpn_password"))
	t.Setenv("BOOT_HEALTH", "unhealthy")
	// Cancel once the VPN health check has run to avoid a two-minute timeout.
	cancelCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- RecoverBoot(cancelCtx, path) }()
	deadline := time.After(5 * time.Second)
	for {
		calls, _ := os.ReadFile(log)
		if strings.Contains(string(calls), "ps --format json gluetun") {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("VPN health check was never reached: %s", calls)
		case err := <-done:
			cancel()
			t.Fatalf("recovery exited early: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("unhealthy VPN reported success")
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "qbittorrent") {
		t.Fatal("started qBittorrent with an unhealthy VPN")
	}
	t.Setenv("BOOT_HEALTH", "healthy")
	if err = RecoverBoot(ctx, path); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(log)
	if !strings.HasSuffix(string(calls), "compose -f - start gluetun\ncompose -f - ps --format json gluetun\ncompose -f - start qbittorrent\n") {
		t.Fatalf("unexpected recovery commands: %s", calls)
	}
	if err = m.remove(ctx, env.ProjectName); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("registration survived destroy: %v", err)
	}
	commands, _ := os.ReadFile(os.Getenv("BOOT_MANAGER_LOG"))
	if !strings.Contains(string(commands), "--user enable "+unitName(env.ProjectName)) || !strings.Contains(string(commands), "--user disable --now "+unitName(env.ProjectName)) {
		t.Fatalf("unexpected systemd operations: %s", commands)
	}
}
