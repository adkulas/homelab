package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/adkulas/homelab/internal/config"
)

func TestNonInteractiveInitSelectsDisabledHardwareTranscoding(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	configuration := strings.ReplaceAll(string(uninitializedConfiguration(t)), "hardwareTranscoding: auto", `hardwareTranscoding: ""`)
	writeFile(t, configPath, []byte(configuration), 0o640)
	answersPath := filepath.Join(temporary, "answers.yaml")
	answers := append(completeAnswers(strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), "Canada", "udp", "service-user", "service-password"), []byte("hardwareTranscoding: disabled\n")...)
	writeFile(t, answersPath, answers, 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'encrypted: true\\n'\n"), 0o700)
	command := exec.Command("go", "run", "./cmd/media-stack", "init", "--environment", "staging", "--config", configPath, "--non-interactive", "--answers", answersPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("non-interactive init failed: %v\n%s", err, output)
	}

	declared, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := declared.Spec.Environments["staging"].HardwareTranscoding; got != "disabled" {
		t.Fatalf("hardwareTranscoding = %q, want disabled", got)
	}
}
