package acceptance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestInitPreservesExistingChoicesOwnershipAndEncryptedSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	configuration := append(uninitializedConfiguration(t), []byte(`    acquisition:
        vpn:
            provider: nordvpn
            protocol: openvpn
            openvpnProtocol: tcp
            server:
                countries: [Iceland]
                categories: [P2P]
            catalogueUpdateInterval: 720h
`)...)
	configuration = []byte(strings.Replace(string(configuration), "runtimeUID: 1000", fmt.Sprintf("runtimeUID: %d", os.Getuid()), 1))
	configuration = []byte(strings.Replace(string(configuration), "runtimeGID: 1000", fmt.Sprintf("runtimeGID: %d", os.Getgid()), 1))
	writeFile(t, configPath, configuration, 0o640)
	secretPath := filepath.Join(temporary, "secrets", "staging.sops.yaml")
	if err := os.Mkdir(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, secretPath, []byte("nordvpn: ENC[existing-ciphertext]\n"), 0o600)
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, []byte(`runtimeUID: 9999
runtimeGID: 9999
timezone: Etc/UTC
country: Canada
serverCategory: P2P
openvpnProtocol: udp
catalogueUpdateInterval: 480h
ageRecipient: age1replacement
serviceUsername: replacement-user
servicePassword: replacement-password
`), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\nprintf 'nordvpn:\n  openvpn:\n    serviceUsername: existing-user\n    servicePassword: existing-password\nprofilarr:\n  apiKey: existing-profilarr-api-key-32-characters\njellyfin:\n  username: household\n  password: existing-jellyfin-password\nqbittorrent:\n  username: household\n  password: existing-qbittorrent-password\n'\n"), 0o700)

	beforeConfig := fileState(t, configPath)
	beforeSecret := fileState(t, secretPath)
	command := exec.Command("go", "run", "./cmd/media-stack", "init",
		"--environment", "staging", "--config", configPath,
		"--non-interactive", "--answers", answersPath,
	)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("repeated media-stack init failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Preserved existing") {
		t.Fatalf("output does not report preservation:\n%s", output)
	}
	if after := fileState(t, configPath); after != beforeConfig {
		t.Fatalf("Declared Configuration changed on repeated initialization\nbefore: %#v\nafter:  %#v", beforeConfig, after)
	}
	if after := fileState(t, secretPath); after != beforeSecret {
		t.Fatalf("encrypted secrets changed on repeated initialization\nbefore: %#v\nafter:  %#v", beforeSecret, after)
	}
}

func TestInitReportsMigrationForExistingSecretWithoutQBittorrentCredentials(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), 0o640)
	secretPath := filepath.Join(temporary, "secrets", "staging.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, secretPath, []byte("encrypted: existing\n"), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\nprintf 'nordvpn:\n  openvpn:\n    serviceUsername: user\n    servicePassword: password\nprofilarr:\n  apiKey: fixture-profilarr-api-key-32-characters\njellyfin:\n  username: household\n  password: jellyfin-password\n'\n"), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "init", "--environment", "staging", "--config", configPath, "--non-interactive", "--answers", filepath.Join(temporary, "unused.yaml"))
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("init accepted an outdated secret document:\n%s", output)
	}
	if !strings.Contains(string(output), "qbittorrent.username") || !strings.Contains(string(output), "qbittorrent.password") {
		t.Fatalf("migration remedy does not name required keys:\n%s", output)
	}
}

type observedFileState struct {
	Contents string
	Mode     os.FileMode
	UID      uint32
	GID      uint32
}

func fileState(t *testing.T, path string) observedFileState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ownership, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("filesystem does not expose Unix ownership for %s", path)
	}
	return observedFileState{
		Contents: string(readFile(t, path)),
		Mode:     info.Mode(),
		UID:      ownership.Uid,
		GID:      ownership.Gid,
	}
}
