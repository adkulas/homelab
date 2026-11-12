package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func destroyCommand(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	command := exec.Command("go", append([]string{"run", "./cmd/media-stack", "destroy"}, arguments...)...)
	command.Dir = repositoryRoot(t)
	return command
}

func TestDestroyRequiresExplicitSelectionAndVolumesForSecrets(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--environment", "staging", "--plan", filepath.Join(t.TempDir(), "plan.json")}, "select --site and --instance"},
		{[]string{"--config", filepath.Join(repositoryRoot(t), "tests/fixtures/media-stack.yaml"), "--plan", filepath.Join(t.TempDir(), "plan.json")}, "environment is required"},
		{[]string{"--environment", "staging", "--config", filepath.Join(repositoryRoot(t), "tests/fixtures/media-stack.yaml"), "--secrets", "--plan", filepath.Join(t.TempDir(), "plan.json")}, "--secrets requires --volumes"},
	} {
		output, err := destroyCommand(t, tc.args...).CombinedOutput()
		if err == nil || !strings.Contains(string(output), tc.want) {
			t.Fatalf("destroy %v: %v\n%s; want %q", tc.args, err, output, tc.want)
		}
	}
}

func TestDestroyPreviewRequiresAnArtifact(t *testing.T) {
	output, err := destroyCommand(t, "--environment", "staging", "--config", filepath.Join(repositoryRoot(t), "tests/fixtures/media-stack.yaml")).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "exactly one of --plan or --execute") {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestDestroyPreviewIsPrivateObservationalAndBoundToExactBytes(t *testing.T) {
	temporary := t.TempDir()
	configPath := backupConfig(t, repositoryRoot(t), temporary)
	dockerDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(dockerDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(temporary, "docker.log")
	writeExecutable(t, filepath.Join(dockerDirectory, "docker"), `#!/bin/sh
printf '%s\n' "$*" >> "$DESTROY_DOCKER_LOG"
case "$1 $2" in
 'context inspect') printf '%s\n' '"unix:///var/run/docker.sock"' ;;
 'info --format') hostname_value=$(hostname); printf '{"ID":"fixture-daemon","Name":"%s"}\n' "$hostname_value" ;;
 'ps --all'|'network ls'|'volume ls') ;;
 *) exit 42 ;;
esac
`)
	artifact := filepath.Join(temporary, "destroy.json")
	run := func(args ...string) ([]byte, error) {
		c := destroyCommand(t, append([]string{"--environment", "staging", "--config", configPath}, args...)...)
		c.Env = append(os.Environ(), "PATH="+dockerDirectory+":"+os.Getenv("PATH"), "DESTROY_DOCKER_LOG="+log)
		return c.CombinedOutput()
	}
	output, err := run("--plan", artifact, "--output", "json")
	if err != nil {
		t.Fatalf("preview: %v\n%s", err, output)
	}
	var report struct {
		SHA256 string `json:"sha256"`
		Plan   struct {
			Environment string    `json:"environment"`
			ExpiresAt   time.Time `json:"expiresAt"`
			CreatedAt   time.Time `json:"createdAt"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, output)
	}
	info, err := os.Stat(artifact)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private plan: %v %v", info, err)
	}
	if report.Plan.Environment != "staging" || report.Plan.ExpiresAt.Sub(report.Plan.CreatedAt) != 30*time.Minute || len(report.SHA256) != 64 {
		t.Fatalf("report: %s", output)
	}
	if _, err := os.Stat(filepath.Join(temporary, "backups", "staging")); !os.IsNotExist(err) {
		t.Fatal("preview wrote backup state")
	}
	contents := readFile(t, artifact)
	if err := os.WriteFile(artifact, append(contents, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = run("--execute", artifact, "--confirm", report.SHA256, "--output", "json")
	if err == nil || !strings.Contains(string(output), "digest") {
		t.Fatalf("modified plan: %v\n%s", err, output)
	}
	for _, line := range strings.Split(string(readFile(t, log)), "\n") {
		if line != "" && !strings.HasPrefix(line, "context inspect") && !strings.HasPrefix(line, "info --format") && !strings.HasPrefix(line, "ps --all") && !strings.HasPrefix(line, "volume ls") && !strings.HasPrefix(line, "network ls") {
			t.Fatalf("preview mutated Docker: %s", line)
		}
	}
	if err := os.WriteFile(artifact, contents, 0600); err != nil {
		t.Fatal(err)
	}
	output, err = run("--execute", artifact, "--confirm", report.SHA256, "--output", "json")
	if err != nil {
		t.Fatalf("execute empty installation: %v\n%s", err, output)
	}
	var execution struct {
		Completed   bool   `json:"completed"`
		JournalPath string `json:"journalPath"`
	}
	if err := json.Unmarshal(output, &execution); err != nil || !execution.Completed || execution.JournalPath == "" {
		t.Fatalf("incomplete execution: %s", output)
	}
	output, err = run("--execute", artifact, "--confirm", report.SHA256, "--output", "json")
	if err != nil {
		t.Fatalf("idempotent repeat: %v\n%s", err, output)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal("destroy removed Declared Configuration")
	}

}
