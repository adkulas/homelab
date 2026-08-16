package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupRejectsUnsupportedPlatformBeforeDownload(t *testing.T) {
	binDirectory := t.TempDir()
	writeExecutable(t, filepath.Join(binDirectory, "uname"), `#!/bin/sh
case "$1" in
  -s) printf '%s\n' Darwin ;;
  -m) printf '%s\n' x86_64 ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "curl"), `#!/bin/sh
printf '%s\n' 'curl must not run' >&2
exit 99
`)

	command := exec.Command("./setup.sh")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("setup.sh succeeded on an unsupported platform:\n%s", output)
	}
	if !strings.Contains(string(output), "unsupported operating system: Darwin") {
		t.Fatalf("setup.sh output = %q", output)
	}
	if strings.Contains(string(output), "curl must not run") {
		t.Fatalf("setup.sh downloaded before rejecting the platform:\n%s", output)
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	writeFile(t, path, []byte(contents), 0o700)
}

func TestSetupRefusesToLaunchDownloadedCLIWhenChecksumFails(t *testing.T) {
	binDirectory := t.TempDir()
	writeExecutable(t, filepath.Join(binDirectory, "uname"), `#!/bin/sh
case "$1" in
  -s) printf '%s\n' Linux ;;
  -m) printf '%s\n' x86_64 ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "curl"), `#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift 2 ;;
    *) url=$1; shift ;;
  esac
done
case "$url" in
  *.sha256) printf '%064d  media-stack_0.1.0_linux_amd64\n' 0 >"$output" ;;
  *) printf '%s\n' '#!/bin/sh' 'printf "CLI MUST NOT RUN\\n"' >"$output" ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "sha256sum"), `#!/bin/sh
printf '%s\n' 'media-stack_0.1.0_linux_amd64: FAILED' >&2
exit 1
`)

	command := setupCommand(t, binDirectory)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("setup.sh accepted a corrupt CLI:\n%s", output)
	}
	if !strings.Contains(string(output), "CLI checksum verification failed") {
		t.Fatalf("setup.sh output = %q", output)
	}
	if strings.Contains(string(output), "CLI MUST NOT RUN") {
		t.Fatalf("setup.sh launched the CLI after checksum failure:\n%s", output)
	}
}

func setupCommand(t *testing.T, binDirectory string, arguments ...string) *exec.Cmd {
	t.Helper()
	command := exec.Command("./setup.sh", arguments...)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_CACHE_HOME="+t.TempDir(),
	)
	return command
}

func TestSetupLaunchesPinnedCLIInitAfterChecksumVerification(t *testing.T) {
	binDirectory := t.TempDir()
	invocationPath := filepath.Join(t.TempDir(), "invocation")
	downloadLogPath := filepath.Join(t.TempDir(), "downloads")
	writeExecutable(t, filepath.Join(binDirectory, "uname"), `#!/bin/sh
case "$1" in
  -s) printf '%s\n' Linux ;;
  -m) printf '%s\n' x86_64 ;;
  -r) printf '%s\n' '5.15.153.1-microsoft-standard-WSL2' ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "curl"), `#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
printf '%s\n' "$url" >>"$DOWNLOAD_LOG"
case "$url" in
  *.sha256) printf '%064d  media-stack_0.1.0_linux_amd64\n' 0 >"$output" ;;
  *) printf '%s\n' '#!/bin/sh' 'printf "%s\\n" "$@" >"$INVOCATION_FILE"' 'printf "%s\\n" "initialized by pinned CLI"' >"$output" ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "sha256sum"), `#!/bin/sh
printf '%s\n' verified >"$CHECKSUM_LOG"
printf '%064d  %s\n' 0 "$1"
exit 0
`)
	checksumLogPath := filepath.Join(t.TempDir(), "checksum")

	command := setupCommand(t, binDirectory, "--environment", "staging")
	command.Env = append(command.Env,
		"INVOCATION_FILE="+invocationPath,
		"DOWNLOAD_LOG="+downloadLogPath,
		"CHECKSUM_LOG="+checksumLogPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("setup.sh failed: %v\n%s", err, output)
	}
	if got := string(readFile(t, checksumLogPath)); got != "verified\n" {
		t.Fatalf("checksum verification log = %q", got)
	}
	if got := string(readFile(t, invocationPath)); got != "init\n--environment\nstaging\n" {
		t.Fatalf("CLI arguments = %q", got)
	}
	if got := string(output); got != "initialized by pinned CLI\n" {
		t.Fatalf("setup.sh output = %q", got)
	}
	downloads := string(readFile(t, downloadLogPath))
	for _, suffix := range []string{
		"/v0.1.0/media-stack_0.1.0_linux_amd64\n",
		"/v0.1.0/media-stack_0.1.0_linux_amd64.sha256\n",
	} {
		if !strings.Contains(downloads, suffix) {
			t.Fatalf("downloads = %q, missing pinned asset suffix %q", downloads, suffix)
		}
	}
}

func TestSetupRejectsChecksumForDifferentAsset(t *testing.T) {
	binDirectory := t.TempDir()
	writeExecutable(t, filepath.Join(binDirectory, "uname"), `#!/bin/sh
case "$1" in
  -s) printf '%s\n' Linux ;;
  -m) printf '%s\n' x86_64 ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "curl"), `#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
case "$url" in
  *.sha256) printf '%064d  different-asset\n' 0 >"$output" ;;
  *) printf '%s\n' '#!/bin/sh' 'printf "CLI MUST NOT RUN\\n"' >"$output" ;;
esac
`)
	writeExecutable(t, filepath.Join(binDirectory, "sha256sum"), `#!/bin/sh
printf '%064d  %s\n' 0 "$1"
exit 0
`)

	command := setupCommand(t, binDirectory)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("setup.sh accepted a checksum for another asset:\n%s", output)
	}
	if !strings.Contains(string(output), "CLI checksum verification failed") {
		t.Fatalf("setup.sh output = %q", output)
	}
	if strings.Contains(string(output), "CLI MUST NOT RUN") {
		t.Fatalf("setup.sh launched a CLI not named by the checksum:\n%s", output)
	}
}
