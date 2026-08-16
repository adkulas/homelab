package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRejectsInvalidTimezoneBeforeWritingConfigurationOrSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml"), configPath, 0o640)
	before := fileState(t, configPath)
	answersPath := filepath.Join(temporary, "answers.yaml")
	answers := strings.Replace(string(completeAnswers("1234", "2345", "Canada", "udp", "service-user", "service-password")),
		"timezone: America/Toronto", "timezone: Mars/Olympus", 1)
	writeFile(t, answersPath, []byte(answers), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'encrypted: ENC[ciphertext]\\n'\n"), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "init",
		"--environment", "staging", "--config", configPath,
		"--non-interactive", "--answers", answersPath,
	)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack init accepted an invalid timezone:\n%s", output)
	}
	if !strings.Contains(string(output), "timezone") {
		t.Fatalf("failure is not actionable:\n%s", output)
	}
	if after := fileState(t, configPath); after != before {
		t.Fatalf("invalid initialization changed Declared Configuration\nbefore: %#v\nafter:  %#v", before, after)
	}
	if _, err := os.Stat(filepath.Join(temporary, "secrets", "staging.sops.yaml")); !os.IsNotExist(err) {
		t.Fatalf("invalid initialization created a secret document: %v", err)
	}
}
