package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInitPreservesDeclaredChoicesWhenAddingAnotherEnvironmentSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyUninitializedConfig(t, configPath, 0o640)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'encrypted: ENC[ciphertext]\\n'\n"), 0o700)

	stagingAnswers := filepath.Join(temporary, "staging-answers.yaml")
	writeFile(t, stagingAnswers, completeAnswers(strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), "Iceland", "tcp", "staging-user", "staging-password"), 0o600)
	runNonInteractiveInit(t, binDirectory, configPath, "staging", stagingAnswers)
	afterStaging := fileState(t, configPath)
	if !strings.Contains(afterStaging.Contents, "openvpnProtocol: tcp") {
		t.Fatalf("explicit OpenVPN TCP fallback was not persisted:\n%s", afterStaging.Contents)
	}

	productionAnswers := filepath.Join(temporary, "production-answers.yaml")
	writeFile(t, productionAnswers, completeAnswers("9999", "9999", "Canada", "udp", "production-user", "production-password"), 0o600)
	runNonInteractiveInit(t, binDirectory, configPath, "production", productionAnswers)
	if afterProduction := fileState(t, configPath); afterProduction != afterStaging {
		t.Fatalf("adding Production secrets replaced existing Declared Configuration choices\nbefore: %#v\nafter:  %#v", afterStaging, afterProduction)
	}
	readFile(t, filepath.Join(temporary, "secrets", "production.sops.yaml"))
}

func completeAnswers(uid, gid, country, protocol, username, password string) []byte {
	return []byte("runtimeUID: " + uid + "\n" +
		"runtimeGID: " + gid + "\n" +
		"timezone: America/Toronto\n" +
		"country: " + country + "\n" +
		"serverCategory: P2P\n" +
		"openvpnProtocol: " + protocol + "\n" +
		"catalogueUpdateInterval: 480h\n" +
		"ageRecipient: age1example\n" +
		"serviceUsername: " + username + "\n" +
		"servicePassword: " + password + "\n" +
		"profilarrAPIKey: fixture-profilarr-api-key-32-characters\n" +
		"jellyfinUsername: household\n" +
		"jellyfinPassword: fixture-jellyfin-password\n")
}

func runNonInteractiveInit(t *testing.T, binDirectory, configPath, environment, answersPath string) {
	t.Helper()
	command := exec.Command("go", "run", "./cmd/media-stack", "init",
		"--environment", environment, "--config", configPath,
		"--non-interactive", "--answers", answersPath,
	)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize %s: %v\n%s", environment, err, output)
	}
}
