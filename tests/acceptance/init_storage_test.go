package acceptance_test

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInitProvisionsOnlyTheSelectedEnvironmentDataLayout(t *testing.T) {
	temporary := t.TempDir()
	stagingRoot := filepath.Join(temporary, "data", "staging")
	productionRoot := filepath.Join(temporary, "data", "production")
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeUninitializedConfigWithDataRoots(t, configPath, productionRoot, stagingRoot)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'encrypted: ENC[ciphertext]\\n'\n"), 0o700)
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, completeAnswers(
		strconv.Itoa(os.Getuid()),
		strconv.Itoa(os.Getgid()),
		"Canada", "udp", "staging-user", "staging-password",
	), 0o600)

	runNonInteractiveInit(t, binDirectory, configPath, "staging", answersPath)

	for _, relative := range []string{
		".",
		"torrents",
		"media",
		filepath.Join("torrents", "movies"),
		filepath.Join("torrents", "series"),
		filepath.Join("media", "movies"),
		filepath.Join("media", "series"),
	} {
		assertDirectoryIdentity(t, filepath.Join(stagingRoot, relative), os.Getuid(), os.Getgid(), 0o750)
	}
	missingDirectory := filepath.Join(stagingRoot, "media", "series")
	if err := os.Remove(missingDirectory); err != nil {
		t.Fatal(err)
	}
	runNonInteractiveInit(t, binDirectory, configPath, "staging", answersPath)
	assertDirectoryIdentity(t, missingDirectory, os.Getuid(), os.Getgid(), 0o750)

	if _, err := os.Stat(productionRoot); !os.IsNotExist(err) {
		t.Fatalf("unselected Production data root exists or could not be inspected: %v", err)
	}
}

func TestInitDoesNotChangeExistingDataOwnershipOrPermissions(t *testing.T) {
	temporary := t.TempDir()
	stagingRoot := filepath.Join(temporary, "staging")
	existingDirectory := filepath.Join(stagingRoot, "torrents", "movies")
	if err := os.MkdirAll(existingDirectory, 0o701); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existingDirectory, 0o701); err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(existingDirectory, "keep.txt")
	writeFile(t, sentinelPath, []byte("existing media\n"), 0o640)
	directoryBefore := fileOwnership(t, existingDirectory)
	fileBefore := fileOwnership(t, sentinelPath)

	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeUninitializedConfigWithDataRoots(t, configPath, filepath.Join(temporary, "production"), stagingRoot)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'encrypted: ENC[ciphertext]\\n'\n"), 0o700)
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, completeAnswers(
		strconv.Itoa(os.Getuid()),
		strconv.Itoa(os.Getgid()),
		"Canada", "udp", "staging-user", "staging-password",
	), 0o600)

	runNonInteractiveInit(t, binDirectory, configPath, "staging", answersPath)

	if after := fileOwnership(t, existingDirectory); after != directoryBefore {
		t.Fatalf("existing directory metadata changed: before %#v, after %#v", directoryBefore, after)
	}
	if after := fileOwnership(t, sentinelPath); after != fileBefore {
		t.Fatalf("existing file metadata changed: before %#v, after %#v", fileBefore, after)
	}
	if got := string(readFile(t, sentinelPath)); got != "existing media\n" {
		t.Fatalf("existing file contents = %q", got)
	}
}

func writeUninitializedConfigWithDataRoots(t *testing.T, path, productionRoot, stagingRoot string) {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(uninitializedConfiguration(t), &document); err != nil {
		t.Fatal(err)
	}
	spec := document["spec"].(map[string]any)
	environments := spec["environments"].(map[string]any)
	environments["production"].(map[string]any)["dataRoot"] = productionRoot
	environments["staging"].(map[string]any)["dataRoot"] = stagingRoot
	contents, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, contents, 0o640)
}

func assertDirectoryIdentity(t *testing.T, path string, uid, gid int, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	metadata := info.Sys().(*syscall.Stat_t)
	if int(metadata.Uid) != uid || int(metadata.Gid) != gid || info.Mode().Perm() != mode {
		t.Fatalf("%s identity = %d:%d %#o, want %d:%d %#o", path, metadata.Uid, metadata.Gid, info.Mode().Perm(), uid, gid, mode)
	}
}

type ownership struct {
	uid  uint32
	gid  uint32
	mode os.FileMode
}

func fileOwnership(t *testing.T, path string) ownership {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata := info.Sys().(*syscall.Stat_t)
	return ownership{uid: metadata.Uid, gid: metadata.Gid, mode: info.Mode().Perm()}
}
