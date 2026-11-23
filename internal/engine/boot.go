package engine

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

//go:embed media-stack-recover@.service
var recoveryTemplate string

type bootManifest struct {
	DockerEndpoint   string
	Declared         config.MediaStack
	Environment      string
	ConfigPath       string
	RuntimeDirectory string
	AgeKeyFile       string
	ToolPath         string
	Compose          string
}

func nativeBootRecovery() bool {
	if os.Getenv("SYSTEMD_OFFLINE") == "1" {
		return false
	}
	kernel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	_, err := os.Stat("/run/systemd/system")
	return err == nil && !strings.Contains(strings.ToLower(string(kernel)), "microsoft")
}

type bootManager struct {
	configDir, unitDir string
	user               bool
}

func localBootManager() (bootManager, error) {
	if os.Geteuid() == 0 {
		return bootManager{"/etc", "/etc/systemd/system", false}, nil
	}
	dir, err := os.UserConfigDir()
	return bootManager{dir, filepath.Join(dir, "systemd/user"), true}, err
}

func (m bootManager) control(ctx context.Context, args ...string) error {
	if m.user {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

func (m bootManager) directory(project string) string {
	return filepath.Join(m.configDir, "media-stack/boot", project)
}
func unitName(project string) string { return "media-stack-recover@" + project + ".service" }

func installRecovery(ctx context.Context, req PlanRequest, declared config.MediaStack, plan Plan) error {
	env := declared.Spec.Environments[req.environment]
	if env.ProjectName == "" {
		return fmt.Errorf("boot recovery requires a Compose project name")
	}
	m, err := localBootManager()
	if err != nil {
		return err
	}
	return m.install(ctx, req, declared, plan)
}

func (m bootManager) install(ctx context.Context, req PlanRequest, declared config.MediaStack, plan Plan) error {
	env := declared.Spec.Environments[req.environment]
	endpoint, err := localDockerEndpoint(ctx)
	if err != nil {
		return err
	}
	keyFile := os.Getenv("SOPS_AGE_KEY_FILE")
	if keyFile != "" {
		keyFile, err = filepath.Abs(keyFile)
		if err != nil {
			return err
		}
	}
	template := recoveryTemplate
	if m.user {
		// The user manager starts at boot with lingering and retries until Docker is ready.
		out, err := exec.CommandContext(ctx, "loginctl", "enable-linger", fmt.Sprint(os.Getuid())).CombinedOutput()
		if err != nil {
			return fmt.Errorf("enable deployment user lingering: %w: %s", err, bytes.TrimSpace(out))
		}
		template = strings.Replace(template, "After=docker.service network-online.target\nWants=network-online.target\nPartOf=docker.service\n", "", 1)
		template = strings.Replace(template, "WantedBy=docker.service", "WantedBy=default.target", 1)
	}
	dir := m.directory(env.ProjectName)
	// Snapshot the applied topology and worker so boot never executes a newer checkout.
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "media-stack"), data, 0700); err != nil {
		return err
	}
	// The system unit uses the Host's administrator-installed tools.
	toolPath := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if m.user {
		for _, tool := range []string{"sops", "docker"} {
			path, err := exec.LookPath(tool)
			if err != nil {
				return err
			}
			toolPath = filepath.Dir(path) + ":" + toolPath
		}
	}
	manifest := bootManifest{
		Declared: declared, Environment: req.environment, ConfigPath: req.configPath,
		RuntimeDirectory: environmentRuntimeSecretDirectory(env), Compose: string(plan.compose),
		AgeKeyFile: keyFile, ToolPath: toolPath, DockerEndpoint: endpoint,
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err = writeSecretAtomic(filepath.Join(dir, "recovery.json"), data); err != nil {
		return err
	}
	if err = writeFileAtomic(filepath.Join(m.unitDir, "media-stack-recover@.service"), []byte(template), 0644); err != nil {
		return err
	}
	if err = m.control(ctx, "daemon-reload"); err != nil {
		return err
	}
	return m.control(ctx, "enable", unitName(env.ProjectName))
}

func removeRecovery(ctx context.Context, project string) error {
	m, err := localBootManager()
	if err != nil {
		return err
	}
	return m.remove(ctx, project)
}

func (m bootManager) remove(ctx context.Context, project string) error {
	if project == "" {
		return nil
	}
	dir := m.directory(project)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := m.control(ctx, "disable", "--now", unitName(project)); err != nil {
		return err
	}
	for _, name := range []string{"recovery.json", "media-stack"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(dir)
}

// RecoverBoot runs in the deployment account's oneshot process.
func RecoverBoot(ctx context.Context, path string) error {
	data, err := readPrivateArtifact(path)
	if err != nil {
		return err
	}
	var m bootManifest
	if err = json.Unmarshal(data, &m); err != nil {
		return err
	}
	req := PlanRequest{environment: m.Environment, configPath: m.ConfigPath, declared: &m.Declared}
	release, err := lockEnvironment(ctx, req, false)
	if err != nil {
		return err
	}
	defer release()
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, current) {
		return fmt.Errorf("boot registration changed; retry")
	}
	env := m.Declared.Spec.Environments[m.Environment]
	gate, _ := restoreDrillGatePaths(env.BackupRoot)
	if _, err = os.Stat(gate); err == nil {
		return fmt.Errorf("restore drill awaits explicit apply")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Unsetenv("DOCKER_CONTEXT"); err != nil {
		return err
	}
	if err = os.Setenv("DOCKER_HOST", m.DockerEndpoint); err != nil {
		return err
	}
	if err = os.Setenv("PATH", m.ToolPath); err != nil {
		return err
	}
	if m.AgeKeyFile != "" {
		if err = os.Setenv("SOPS_AGE_KEY_FILE", m.AgeKeyFile); err != nil {
			return err
		}
	}
	secrets, err := decryptSelectedEnvironmentSecrets(ctx, m.ConfigPath, env)
	if err != nil {
		return err
	}
	return startVPNPair(ctx, Plan{compose: []byte(m.Compose)}, m.RuntimeDirectory, secrets, []string{"start"})
}
