package acceptance_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInitEncryptsUserChosenCompanionCredentials(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyUninitializedConfig(t, configPath, 0600)
	answers := completeAnswers(strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()), "Canada", "udp", "service-user", "service-password")
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, answers, 0600)
	bin := filepath.Join(temporary, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	captured := filepath.Join(temporary, "sops-input.yaml")
	writeExecutable(t, filepath.Join(bin, "sops"), `#!/bin/sh
cat > "$BOOTSTRAP_CAPTURE"
printf 'encrypted: ENC[fixture-ciphertext]\n'
`)
	command := exec.Command("go", "run", "./cmd/media-stack", "init", "--environment", "staging", "--config", configPath, "--non-interactive", "--answers", answersPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "BOOTSTRAP_CAPTURE="+captured)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, output)
	}
	var document map[string]map[string]any
	if err := yaml.Unmarshal(readFile(t, captured), &document); err != nil {
		t.Fatal(err)
	}
	if document["houndarr"]["username"] != "chosen-hound-admin" || document["houndarr"]["password"] != "chosen-hound-password" || document["cleanuparr"]["username"] != "chosen-clean-admin" || document["cleanuparr"]["password"] != "chosen-clean-password" {
		t.Fatal("chosen companion credentials were not sent to SOPS")
	}
	if strings.Contains(string(output), "chosen-hound-password") || strings.Contains(string(output), "chosen-clean-password") {
		t.Fatal("init displayed a password")
	}
	encrypted := readFile(t, filepath.Join(temporary, "secrets/staging.sops.yaml"))
	if strings.Contains(string(encrypted), "chosen-") {
		t.Fatal("plaintext credentials written to the secret document")
	}
}

type companionBootstrapFixture struct {
	failOnce                                               string
	accounts                                               int
	houndUser, houndPassword, cleanupUser, cleanupPassword string
	houndEnabled, houndInstance, cleanupComplete           bool
	houndFields                                            url.Values
	storedConnectionTested                                 bool
}

func (fixture *companionBootstrapFixture) serveCleanupAccount(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/auth/status" {
		_ = json.NewEncoder(w).Encode(map[string]bool{"setupCompleted": fixture.cleanupComplete})
		return true
	}
	if r.URL.Path == "/api/auth/setup/account" {
		if fixture.cleanupUser != "" {
			http.Error(w, "Account already exists", 409)
			return true
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		fixture.cleanupUser, fixture.cleanupPassword = body["username"], body["password"]
		fixture.accounts++
		w.WriteHeader(201)
		fmt.Fprint(w, `{"userId":"fixture-account"}`)
		return true
	}
	if r.URL.Path == "/api/auth/setup/complete" {
		fixture.cleanupComplete = true
		fmt.Fprint(w, `{}`)
		return true
	}
	if r.URL.Path == "/api/auth/login" {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !fixture.cleanupComplete || body["username"] != fixture.cleanupUser || body["password"] != fixture.cleanupPassword {
			http.Error(w, "unauthorized", 401)
			return true
		}
		fmt.Fprint(w, `{"tokens":{"accessToken":"fixture-access-token"}}`)
		return true
	}
	if r.URL.Path == "/api/account/api-key" {
		if r.Header.Get("Authorization") != "Bearer fixture-access-token" {
			http.Error(w, "unauthorized", 401)
			return true
		}
		fmt.Fprint(w, `{"apiKey":"fixture-cleanuparr-key"}`)
		return true
	}
	return false
}

func (fixture *companionBootstrapFixture) serveHound(w http.ResponseWriter, r *http.Request, mode string) bool {
	if r.Method == "POST" && r.URL.Path == fixture.failOnce {
		fixture.failOnce = ""
		http.Error(w, "interrupted fixture", 500)
		return true
	}

	if r.URL.Path == "/setup" {
		if r.Method == "GET" {
			if fixture.houndUser != "" {
				http.Redirect(w, r, "/login", 302)
			} else {
				fmt.Fprint(w, `<form action="/setup"><input name="password_confirm"></form>`)
			}
			return true
		}
		_ = r.ParseForm()
		if fixture.houndUser != "" {
			http.Redirect(w, r, "/login", 302)
			return true
		}
		if r.Form.Get("password") != r.Form.Get("password_confirm") {
			http.Error(w, "mismatch", 422)
			return true
		}
		fixture.houndUser, fixture.houndPassword = r.Form.Get("username"), r.Form.Get("password")
		fixture.accounts++
		http.Redirect(w, r, "/login", 303)
		return true
	}
	if r.URL.Path == "/login" {
		_ = r.ParseForm()
		if r.Form.Get("username") != fixture.houndUser || r.Form.Get("password") != fixture.houndPassword || fixture.houndUser == "" {
			http.Error(w, "unauthorized", 401)
			return true
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/"})
		http.Redirect(w, r, "/", 303)
		return true
	}
	if _, err := r.Cookie("session"); err != nil {
		http.Error(w, "unauthorized", 401)
		return true
	}
	if r.Method == "POST" && r.Header.Get("X-CSRF-Token") != "fixture-csrf" {
		http.Error(w, "csrf", 403)
		return true
	}
	switch r.URL.Path {
	case "/settings":
		fmt.Fprint(w, `<input type="hidden" name="csrf_token" value="fixture-csrf"><table>`)
		if fixture.houndInstance {
			state := "Enable"
			if fixture.houndEnabled {
				state = "Disable"
			}
			fmt.Fprintf(w, `<tr id="instance-row-1"><td><button hx-post="/settings/instances/1/toggle-enabled">%s</button><button hx-get="/settings/instances/1/edit">Edit</button></td></tr>`, state)
		}
		fmt.Fprint(w, `</table>`)
		return true
	case "/settings/instances", "/settings/instances/1":
		_ = r.ParseForm()
		fixture.houndFields = r.Form
		fixture.storedConnectionTested = false
		if !fixture.houndInstance {
			if r.Form.Get("missing_enabled") == "on" {
				http.Error(w, "new instance must start paused", 400)
				return true
			}
			fixture.houndInstance = true
			fixture.houndEnabled = true
		}
		fmt.Fprint(w, `<tr id="instance-row-1"><td><button hx-get="/settings/instances/1/edit">Edit</button></td></tr>`)
		return true
	case "/settings/instances/1/toggle-enabled":
		if !fixture.houndEnabled && !fixture.storedConnectionTested {
			http.Error(w, "saved connection must be tested before enabling", 400)
			return true
		}
		fixture.houndEnabled = !fixture.houndEnabled
		fmt.Fprint(w, `<div>updated</div>`)
		return true
	case "/settings/instances/1/edit":
		fmt.Fprint(w, `<form>`)
		for key, values := range fixture.houndFields {
			value := values[0]
			if key == "type" || key == "sonarr_search_mode" || key == "search_order" {
				if key == "sonarr_search_mode" {
					value = mode
				}
				fmt.Fprintf(w, `<select name="%s"><option value="%s" selected>Value</option></select>`, key, value)
				continue
			}
			if key == "missing_enabled" || key == "cutoff_enabled" || key == "upgrade_enabled" {
				checked := ""
				if value == "on" {
					checked = "checked"
				}
				fmt.Fprintf(w, `<input type="checkbox" name="%s" %s>`, key, checked)
				continue
			}
			fmt.Fprintf(w, `<input name="%s" value="%s">`, key, value)
		}
		fmt.Fprint(w, `</form>`)
		return true
	case "/settings/instances/test-connection":
		_ = r.ParseForm()
		if fixture.houndInstance && !fixture.houndEnabled && r.Form.Get("api_key") == "__UNCHANGED__" && r.Form.Get("instance_id") == "1" {
			fixture.storedConnectionTested = true
		}
		w.Header().Set("HX-Trigger", "houndarr-connection-test-success")
		fmt.Fprint(w, "Connection successful")
		return true
	}
	return false
}

func installBootstrapSOPSFixture(t *testing.T, bin, secretPath string) {
	t.Helper()
	document := map[string]any{"nordvpn": map[string]any{"openvpn": map[string]string{"serviceUsername": "apply-service-user", "servicePassword": "apply-service-password"}}, "profilarr": map[string]string{"apiKey": "fixture-profilarr-api-key-32-characters"}, "jellyfin": map[string]string{"username": "household", "password": "fixture-jellyfin-password"}, "qbittorrent": map[string]string{"username": "household", "password": "fixture-qbittorrent-password"}, "houndarr": map[string]string{"username": "chosen-hound-admin", "password": "chosen-hound-password"}, "cleanuparr": map[string]string{"username": "chosen-clean-admin", "password": "chosen-clean-password"}}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, secretPath, encoded, 0600)
	writeExecutable(t, filepath.Join(bin, "sops"), `#!/usr/bin/env python3
import sys,json
from pathlib import Path
if sys.argv[1]=='decrypt':
 print(Path(sys.argv[-1]).read_text())
elif sys.argv[1]=='set':
 path=Path(sys.argv[-2]); document=json.loads(path.read_text())
 document[json.loads(sys.argv[-1])[0]]=json.load(sys.stdin)
 path.write_text(json.dumps(document))
else:
 sys.exit(99)
`)
}

func TestCompanionInitMigratesEncryptedSecretsWithoutChangingExistingChoices(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	copyUninitializedConfig(t, configPath, 0600)
	configBefore := readFile(t, configPath)
	identity, recipient := newAgeIdentity(t)
	plaintext := `nordvpn:
  openvpn:
    serviceUsername: preserve-service
    servicePassword: preserve-password
profilarr:
  apiKey: preserve-profilarr-key-32-characters
jellyfin:
  username: preserve-jellyfin
  password: preserve-jellyfin-password
qbittorrent:
  username: preserve-qbit
  password: preserve-qbit-password
houndarr:
  username: existing-hound
  password: existing-hound-password
`
	secretPath := filepath.Join(temporary, "secrets/staging.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0700); err != nil {
		t.Fatal(err)
	}
	encrypt := exec.Command("sops", "encrypt", "--age", recipient, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	encrypt.Stdin = strings.NewReader(plaintext)
	encrypted, err := encrypt.Output()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, secretPath, encrypted, 0600)
	answersPath := filepath.Join(temporary, "answers.yaml")
	writeFile(t, answersPath, []byte("houndarrUsername: ignored-change\nhoundarrPassword: ignored-password\ncleanuparrUsername: chosen-clean-admin\ncleanuparrPassword: chosen-clean-password\n"), 0600)
	run := func() {
		t.Helper()
		command := exec.Command("go", "run", "./cmd/media-stack", "init", "--companions", "--environment", "staging", "--config", configPath, "--non-interactive", "--answers", answersPath)
		command.Dir = repositoryRoot(t)
		command.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("companion init: %v\n%s", err, output)
		}
		if strings.Contains(string(output), "chosen-clean-password") {
			t.Fatal("password appeared in output")
		}
	}
	// A failed encrypted patch must leave the entire original document intact.
	failureBin := filepath.Join(temporary, "fail-bin")
	if err := os.Mkdir(failureBin, 0700); err != nil {
		t.Fatal(err)
	}
	realSOPS, err := exec.LookPath("sops")
	if err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(failureBin, "sops"), "#!/bin/sh\nif [ \"$1\" = set ]; then exit 1; fi\nexec \"$REAL_SOPS\" \"$@\"\n")
	failed := exec.Command("go", "run", "./cmd/media-stack", "init", "--companions", "--environment", "staging", "--config", configPath, "--non-interactive", "--answers", answersPath)
	failed.Dir = repositoryRoot(t)
	failed.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity, "REAL_SOPS="+realSOPS, "PATH="+failureBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := failed.CombinedOutput(); err == nil || !strings.Contains(string(output), "original document preserved") {
		t.Fatalf("expected encrypted patch failure: %v\n%s", err, output)
	}
	if string(encrypted) != string(readFile(t, secretPath)) {
		t.Fatal("failed migration changed the original SOPS document")
	}
	// Use a real terminal so echo handling is exercised, not merely pipe input.
	interactive := exec.Command("python3", "-c", `
import os,pty,select,subprocess,sys,termios,time
master,slave=pty.openpty()
process=subprocess.Popen(['go','run','./cmd/media-stack','init','--companions','--environment','staging','--config',sys.argv[1]],stdin=slave,stdout=slave,stderr=slave)
transcript=b''
try:
 for label,value,hidden in [(b'Cleanuparr administrator username: ',b'chosen-clean-admin',False),(b'Cleanuparr administrator password: ',b'chosen-clean-password',True),(b'Confirm Cleanuparr administrator password: ',b'chosen-clean-password',True)]:
  deadline=time.monotonic()+30
  while label not in transcript:
   if time.monotonic()>deadline: raise RuntimeError('terminal prompt timed out')
   if select.select([master],[],[],0.1)[0]: transcript+=os.read(master,4096)
  if hidden:
   deadline=time.monotonic()+5
   while termios.tcgetattr(slave)[3]&termios.ECHO and time.monotonic()<deadline: time.sleep(0.01)
   if termios.tcgetattr(slave)[3]&termios.ECHO: raise RuntimeError('password echo enabled')
  os.write(master,value+b'\n')
 deadline=time.monotonic()+30
 while process.poll() is None:
  if time.monotonic()>deadline: raise RuntimeError('terminal init timed out')
  if select.select([master],[],[],0.1)[0]: transcript+=os.read(master,4096)
 while select.select([master],[],[],0)[0]: transcript+=os.read(master,4096)
 if process.returncode: raise RuntimeError('terminal init failed: '+transcript.decode())
 if b'chosen-clean-password' in transcript: raise RuntimeError('terminal echoed password')
finally:
 if process.poll() is None: process.kill();process.wait()
 os.close(master);os.close(slave)
`, configPath)
	interactive.Dir = repositoryRoot(t)
	interactive.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity)
	if output, err := interactive.CombinedOutput(); err != nil {
		t.Fatalf("terminal companion init: %v\n%s", err, output)
	}
	after := readFile(t, secretPath)
	if strings.Contains(string(after), "chosen-clean") || strings.Contains(string(after), "existing-hound-password") {
		t.Fatal("SOPS file contains plaintext credentials")
	}
	decrypt := exec.Command("sops", "decrypt", "--output-type", "yaml", secretPath)
	decrypt.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+identity)
	decoded, err := decrypt.Output()
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := yaml.Unmarshal(decoded, &actual); err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := yaml.Unmarshal([]byte(plaintext), &original); err != nil {
		t.Fatal(err)
	}
	for branch, value := range original {
		left, _ := json.Marshal(actual[branch])
		right, _ := json.Marshal(value)
		if string(left) != string(right) {
			t.Fatalf("existing %s changed", branch)
		}
	}
	cleanup := actual["cleanuparr"].(map[string]any)
	if cleanup["username"] != "chosen-clean-admin" || cleanup["password"] != "chosen-clean-password" {
		t.Fatal("chosen credentials not encrypted")
	}
	if string(configBefore) != string(readFile(t, configPath)) {
		t.Fatal("companion migration changed declared configuration")
	}
	if !strings.Contains(string(after), recipient) {
		t.Fatal("SOPS recipient changed")
	}
	run()
	if string(after) != string(readFile(t, secretPath)) {
		t.Fatal("repeated migration rewrote existing credentials")
	}
}

func TestFreshInitRequiresChosenCompanionCredentials(t *testing.T) {
	for _, account := range []string{"houndarr", "cleanuparr"} {
		t.Run(account, func(t *testing.T) {
			temporary := t.TempDir()
			configPath := filepath.Join(temporary, "media-stack.yaml")
			copyUninitializedConfig(t, configPath, 0600)
			answers := string(completeAnswers("1234", "2345", "Canada", "udp", "service-user", "service-password"))
			lines := strings.Split(answers, "\n")
			var kept []string
			for _, line := range lines {
				if !strings.HasPrefix(line, account+"Username:") && !strings.HasPrefix(line, account+"Password:") {
					kept = append(kept, line)
				}
			}
			answersPath := filepath.Join(temporary, "answers.yaml")
			writeFile(t, answersPath, []byte(strings.Join(kept, "\n")), 0600)
			bin := filepath.Join(temporary, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(bin, "sops"), "#!/bin/sh\ncat >/dev/null\nprintf 'encrypted: ENC[fixture]\\n'\n")
			command := exec.Command("go", "run", "./cmd/media-stack", "init", "--environment", "staging", "--config", configPath, "--non-interactive", "--answers", answersPath)
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(strings.ToLower(string(output)), account) {
				t.Fatalf("expected missing chosen credentials to stop init: %v\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(temporary, "secrets/staging.sops.yaml")); !os.IsNotExist(err) {
				t.Fatal("missing chosen credentials wrote secrets")
			}
		})
	}
}
