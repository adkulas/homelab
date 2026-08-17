package acceptance_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyStartsQBittorrentOnlyAfterHealthyGluetunWithRuntimeSecrets(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "media-stack.yaml")
	preferences := map[string]any{}
	categories := map[string]map[string]string{}
	var rootFolders []map[string]any
	var downloadClients []map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/v3/") {
			if request.Header.Get("X-Api-Key") != "fixture-radarr-api-key" {
				http.Error(writer, "Unauthorized", http.StatusUnauthorized)
				return
			}
			switch request.Method + " " + request.URL.Path {
			case "GET /api/v3/system/status":
				_ = json.NewEncoder(writer).Encode(map[string]string{"appName": "Radarr"})
			case "GET /api/v3/rootfolder":
				_ = json.NewEncoder(writer).Encode(rootFolders)
			case "POST /api/v3/rootfolder":
				var root map[string]any
				_ = json.NewDecoder(request.Body).Decode(&root)
				rootFolders = append(rootFolders, root)
				writer.WriteHeader(http.StatusCreated)
			case "GET /api/v3/downloadclient":
				_ = json.NewEncoder(writer).Encode(downloadClients)
			case "POST /api/v3/downloadclient":
				var client map[string]any
				_ = json.NewDecoder(request.Body).Decode(&client)
				downloadClients = append(downloadClients, client)
				writer.WriteHeader(http.StatusCreated)
			default:
				http.NotFound(writer, request)
			}
			return
		}
		if request.URL.Path == "/api/v2/auth/login" {
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("username") != "admin" || values.Get("password") != "fixture-temporary-password" {
				http.Error(writer, "Fails.", http.StatusForbidden)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "SID", Value: "acceptance-session", Path: "/"})
			_, _ = writer.Write([]byte("Ok."))
			return
		}
		cookie, err := request.Cookie("SID")
		if err != nil || cookie.Value != "acceptance-session" {
			http.Error(writer, "Forbidden", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/api/v2/app/preferences":
			_ = json.NewEncoder(writer).Encode(preferences)
		case "/api/v2/app/setPreferences":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			_ = json.Unmarshal([]byte(values.Get("json")), &preferences)
		case "/api/v2/torrents/categories":
			_ = json.NewEncoder(writer).Encode(categories)
		case "/api/v2/torrents/createCategory":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			name := values.Get("category")
			categories[name] = map[string]string{"name": name, "savePath": values.Get("savePath")}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()
	apiURL, err := url.Parse(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	declared := strings.Replace(string(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml"))), "qbittorrent: 18080", "qbittorrent: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "radarr: 17878", "radarr: "+apiURL.Port(), 1)
	writeFile(t, configPath, []byte(declared), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
cat <<'EOF'
nordvpn:
  openvpn:
    serviceUsername: apply-service-user
    servicePassword: apply-service-password
EOF
`), 0o700)
	dockerLog := filepath.Join(temporary, "docker-arguments")
	composeCapture := filepath.Join(temporary, "compose.yaml")
	healthCount := filepath.Join(temporary, "health-count")
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
	printf '%s\n' "$*" >> "$APPLY_DOCKER_LOG"
	cat > "$APPLY_COMPOSE_CAPTURE"
	case "$*" in
	  "compose -f - up -d gluetun") exit 0 ;;
	  "compose -f - up -d qbittorrent") exit 0 ;;
	  "compose -f - logs --no-color qbittorrent") printf 'A temporary password is provided for this session: fixture-temporary-password\n'; exit 0 ;;
	  "compose -f - up -d radarr") exit 0 ;;
	  "compose -f - exec -T radarr cat /config/config.xml") printf '<Config><ApiKey>fixture-radarr-api-key</ApiKey></Config>\n'; exit 0 ;;
	  "compose -f - ps --format json gluetun")
	    count=0
	    [ -f "$APPLY_HEALTH_COUNT" ] && count=$(cat "$APPLY_HEALTH_COUNT")
	    count=$((count + 1))
	    printf '%s' "$count" > "$APPLY_HEALTH_COUNT"
	    [ "$count" -eq 1 ] && printf '{"Health":"unhealthy","State":"running"}\n' || printf '{"Health":"healthy","State":"running"}\n'
	    exit 0 ;;
	  *) exit 99 ;;
	esac
`), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+filepath.Join(temporary, "runtime"),
		"APPLY_DOCKER_LOG="+dockerLog,
		"APPLY_COMPOSE_CAPTURE="+composeCapture,
		"APPLY_HEALTH_COUNT="+healthCount,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack apply failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Prepared Radarr for the Movie Library in the staging Environment.") {
		t.Errorf("apply output = %q, want completed Radarr phase", output)
	}

	wantDocker := "compose -f - up -d gluetun\ncompose -f - ps --format json gluetun\ncompose -f - ps --format json gluetun\ncompose -f - up -d qbittorrent\ncompose -f - logs --no-color qbittorrent\ncompose -f - up -d radarr\ncompose -f - exec -T radarr cat /config/config.xml"
	if got := strings.TrimSpace(string(readFile(t, dockerLog))); got != wantDocker {
		t.Errorf("Docker invocation = %q", got)
	}
	rendered := string(readFile(t, composeCapture))
	for _, secret := range []string{"apply-service-user", "apply-service-password"} {
		if strings.Contains(rendered, secret) || strings.Contains(string(output), secret) {
			t.Errorf("apply exposed secret %q\noutput: %s\nCompose: %s", secret, output, rendered)
		}
	}
	if strings.Contains(string(output), "fixture-temporary-password") {
		t.Errorf("apply exposed qBittorrent temporary password: %s", output)
	}

	runtimeDirectory := filepath.Join(temporary, "runtime", "media-stack", "media-staging")
	if info, statErr := os.Stat(runtimeDirectory); statErr != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime secret directory mode = %v, %v; want 0700", info, statErr)
	}
	for name, want := range map[string]string{
		"openvpn_user":     "apply-service-user",
		"openvpn_password": "apply-service-password",
	} {
		path := filepath.Join(runtimeDirectory, name)
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("runtime secret %s mode = %v, %v; want 0600", name, info, statErr)
		}
		if got := strings.TrimSpace(string(readFile(t, path))); got != want {
			t.Errorf("runtime secret %s = %q, want selected credential", name, got)
		}
	}
	if preferences["save_path"] != "/data/torrents" || categories["movies"]["savePath"] != "movies" || categories["series"]["savePath"] != "series" {
		t.Errorf("apply did not reconcile qBittorrent acquisition policy: preferences=%v categories=%v", preferences, categories)
	}
	if len(rootFolders) != 1 || rootFolders[0]["path"] != "/data/media/movies" {
		t.Errorf("apply did not reconcile the Movie Library root folder: %v", rootFolders)
	}
	if len(downloadClients) != 1 {
		t.Fatalf("apply did not reconcile Radarr's qBittorrent client: %v", downloadClients)
	}
	fields := map[string]any{}
	for _, item := range downloadClients[0]["fields"].([]any) {
		field := item.(map[string]any)
		fields[field["name"].(string)] = field["value"]
	}
	if fields["host"] != "qbittorrent" || fields["port"] != float64(8080) || fields["movieCategory"] != "movies" {
		t.Errorf("Radarr qBittorrent contract = %v", fields)
	}
	secondCommand := exec.Command("go", "run", "./cmd/media-stack", "apply", "--environment", "staging", "--config", configPath)
	secondCommand.Dir = command.Dir
	secondCommand.Env = command.Env
	secondOutput, err := secondCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("repeated media-stack apply failed: %v\n%s", err, secondOutput)
	}
	if len(rootFolders) != 1 || len(downloadClients) != 1 {
		t.Errorf("repeated apply did not converge: roots=%v downloadClients=%v", rootFolders, downloadClients)
	}
}

func TestApplyRedactsCredentialsFromUnhealthyGluetunFailure(t *testing.T) {
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "media-stack")
	build := exec.Command("go", "build", "-o", binary, "./cmd/media-stack")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build media-stack: %v\n%s", err, output)
	}
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)
	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte(`#!/bin/sh
printf 'nordvpn:\n  openvpn:\n    serviceUsername: failure-service-user\n    servicePassword: failure-service-password\n'
`), 0o700)
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
cat >/dev/null
printf 'authentication failed for failure-service-user with failure-service-password\n' >&2
exit 1
`), 0o700)

	command := exec.Command(binary, "apply", "--environment", "staging", "--config", configPath)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+filepath.Join(temporary, "runtime"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("apply accepted unhealthy Gluetun: %s", output)
	}
	if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
		t.Errorf("unhealthy Gluetun exit = %v, want operational exit 1", err)
	}
	for _, secret := range []string{"failure-service-user", "failure-service-password"} {
		if strings.Contains(string(output), secret) {
			t.Errorf("apply failure exposed secret %q: %s", secret, output)
		}
	}
	if !strings.Contains(string(output), "start healthy Gluetun") || !strings.Contains(string(output), "[REDACTED]") {
		t.Errorf("apply failure is not actionable and redacted: %s", output)
	}
}
