package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInitSendsOnlyOpenVPNServiceCredentialsToSOPS(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyUninitializedConfig(t, configPath, 0o640)
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, completeAnswers(strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), "Canada", "udp", "service-user", "service-password"), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	capturedPath := filepath.Join(temporary, "sops-input.yaml")
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ntee \"$SOPS_CAPTURE\" >/dev/null\nprintf 'encrypted: ENC[ciphertext]\\n'\n"), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "init",
		"--environment", "staging", "--config", configPath,
		"--non-interactive", "--answers", answersPath,
	)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SOPS_CAPTURE="+capturedPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("media-stack init failed: %v\n%s", err, output)
	}
	var got map[string]any
	if err := yaml.Unmarshal(readFile(t, capturedPath), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"nordvpn": map[string]any{
			"openvpn": map[string]any{
				"serviceUsername": "service-user",
				"servicePassword": "service-password",
			},
		},
		"profilarr": map[string]any{
			"apiKey": "fixture-profilarr-api-key-32-characters",
		},
		"jellyfin": map[string]any{
			"username": "household",
			"password": "fixture-jellyfin-password",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plaintext supplied to SOPS = %#v, want %#v", got, want)
	}
}

func TestInitRejectsAccessTokenAnswers(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyUninitializedConfig(t, configPath, 0o640)
	before := fileState(t, configPath)
	answersPath := filepath.Join(temporary, "answers.yaml")
	answers := append(completeAnswers("1234", "2345", "Canada", "udp", "service-user", "service-password"), []byte("accessToken: forbidden-token\n")...)
	writeFile(t, answersPath, answers, 0o600)

	command := exec.Command("go", "run", "./cmd/media-stack", "init",
		"--environment", "staging", "--config", configPath,
		"--non-interactive", "--answers", answersPath,
	)
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("media-stack init accepted an access token answer:\n%s", output)
	}
	if !strings.Contains(string(output), "field accessToken not found") {
		t.Fatalf("access-token rejection is not actionable:\n%s", output)
	}
	if after := fileState(t, configPath); after != before {
		t.Fatalf("rejected access token changed Declared Configuration\nbefore: %#v\nafter:  %#v", before, after)
	}
	if _, err := os.Stat(filepath.Join(temporary, "secrets", "staging.sops.yaml")); !os.IsNotExist(err) {
		t.Fatalf("rejected access token created a secret document: %v", err)
	}
}
