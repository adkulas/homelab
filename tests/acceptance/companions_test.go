package acceptance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestPlanIsolatesSeriesCompanionsWithoutCredentialsOrMediaAccess(t *testing.T) {
	for _, environment := range []string{"production", "staging"} {
		t.Run(environment, func(t *testing.T) {
			output, err := planCommand(t, "--environment", environment).CombinedOutput()
			if err != nil {
				t.Fatalf("plan: %v\n%s", err, output)
			}
			var project struct {
				Services map[string]struct {
					Image       string            `yaml:"image"`
					Environment map[string]string `yaml:"environment"`
					Ports       []string          `yaml:"ports"`
					Volumes     []string          `yaml:"volumes"`
					CapAdd      []string          `yaml:"cap_add"`
					Privileged  bool              `yaml:"privileged"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal(output, &project); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"houndarr", "cleanuparr"} {
				service, ok := project.Services[name]
				if !ok {
					t.Fatalf("%s missing", name)
				}
				if !strings.Contains(service.Image, "@sha256:") {
					t.Errorf("%s image is not pinned", name)
				}
				if len(service.Volumes) != 1 || !strings.HasPrefix(service.Volumes[0], name+"-config:") {
					t.Errorf("%s mounts = %v", name, service.Volumes)
				}
				if len(service.CapAdd) != 0 || service.Privileged {
					t.Errorf("%s unnecessary privileges", name)
				}
				if len(service.Ports) != 1 || !strings.HasPrefix(service.Ports[0], "127.0.0.1:") {
					t.Errorf("%s management ports = %v", name, service.Ports)
				}
				for key := range service.Environment {
					if strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "api_key") {
						t.Errorf("%s renders credential %s", name, key)
					}
				}
			}
		})
	}
}

func TestPlanRejectsUnboundedCompanionSearches(t *testing.T) {
	configuration := string(readFile(t, filepath.Join(repositoryRoot(t), "services/media/media-stack.yaml")))
	configuration = strings.Replace(configuration, "hourlyCap: 12", "hourlyCap: 0", 1)
	if !strings.Contains(configuration, "hourlyCap: 0") {
		t.Fatal("declared companion budget missing")
	}
	path := filepath.Join(t.TempDir(), "media-stack.yaml")
	writeFile(t, path, []byte(configuration), 0600)
	command := exec.Command("go", "run", "./cmd/media-stack", "plan", "--environment", "staging", "--config", path)
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "hourlyCap") {
		t.Fatalf("unbounded searches accepted: %v\n%s", err, output)
	}
}

func TestConfigDescribeExplainsCompanionOwnershipAndSafety(t *testing.T) {
	command := exec.Command("go", "run", "./cmd/media-stack", "config", "describe", "--output", "json")
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("config describe: %v\n%s", err, output)
	}
	for _, want := range []string{"houndarr", "cleanuparr", "proactiveSearchOwner", "replacementSearchOwner", "search.batchSize", "recovery.strikes", "fullyDeselectedTorrents", "chronological"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("contract missing %s", want)
		}
	}
}

// These HTTP fixtures model released public API/form payloads. They do not
// implement the companions' schedulers; disposable tests exercise those images.
type companionAPIFixture struct {
	events         []map[string]any
	hound, cleanup *httptest.Server
	documents      map[string]map[string]any
	rules          map[string][]map[string]any
	writes         int
	mode           string
}

func newCompanionAPIFixture(t *testing.T, qbitPort int) *companionAPIFixture {
	t.Helper()
	fixture := &companionAPIFixture{documents: map[string]map[string]any{}, rules: map[string][]map[string]any{}, mode: "episode"}
	fixture.documents["sonarr"] = map[string]any{"failedImportMaxStrikes": float64(-1), "instances": []any{}}
	fixture.documents["download_client"] = map[string]any{"clients": []any{}}
	fixture.documents["seeker"] = map[string]any{"searchEnabled": true, "searchInterval": float64(2), "proactiveSearchEnabled": true, "instances": []any{}}
	fixture.documents["queue_cleaner"] = map[string]any{"enabled": false}
	for _, name := range []string{"download_cleaner", "malware_blocker", "blacklist_sync"} {
		fixture.documents[name] = map[string]any{"enabled": false}
	}
	fixture.documents["general"] = map[string]any{"dryRun": false, "statusCheckEnabled": true, "ignoredDownloads": []any{}}
	for _, name := range []string{"radarr", "lidarr", "readarr", "whisparr", "sportarr", "lazylibrarian"} {
		fixture.documents[name] = map[string]any{"instances": []any{}}
	}
	fixture.cleanup = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "fixture-cleanuparr-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/api/events" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": fixture.events, "totalPages": 1})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/test") {
			w.WriteHeader(200)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/queue-rules/") {
			kind := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/queue-rules/"), "/")[0]
			if r.Method == "GET" {
				_ = json.NewEncoder(w).Encode(fixture.rules[kind])
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			fixture.writes++
			body["id"] = "fixture-" + kind
			fixture.rules[kind] = []map[string]any{body}
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/configuration/")
		if r.Method == "GET" {
			value, ok := fixture.documents[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		fixture.writes++
		switch {
		case strings.HasPrefix(name, "sonarr/instances"):
			body["id"] = "fixture-sonarr"
			body["apiKey"] = "••••••••"
			fixture.documents["sonarr"]["instances"] = []any{body}
			fixture.documents["seeker"]["instances"] = []any{map[string]any{"arrInstanceId": "fixture-sonarr", "enabled": true}}
		case strings.HasPrefix(name, "download_client"):
			if body["host"] != "http://qbittorrent:"+strconv.Itoa(qbitPort) {
				t.Errorf("wrong qBittorrent target")
			}
			body["id"] = "fixture-qbit"
			body["password"] = "••••••••"
			fixture.documents["download_client"]["clients"] = []any{body}
		case name == "sonarr":
			fixture.documents[name]["failedImportMaxStrikes"] = body["failedImportMaxStrikes"]
		default:
			fixture.documents[name] = body
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	fixture.hound = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			_ = r.ParseForm()
			if r.Form.Get("username") != "companion-admin" || r.Form.Get("password") != "fixture-houndarr-password" {
				http.Error(w, "unauthorized", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/"})
			w.WriteHeader(303)
			return
		}
		if _, err := r.Cookie("session"); err != nil {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/api/logs":
			fmt.Fprint(w, `[{"action":"skipped","reason":"hourly cap reached","item_id":84,"item_type":"episode"}]`)
		case "/settings":
			fmt.Fprint(w, `<input type="hidden" name="csrf_token" value="fixture-csrf"><table><tr id="instance-row-1"><td><button hx-post="/settings/instances/1/toggle-enabled">Disable</button><button hx-get="/settings/instances/1/edit">Edit</button></td></tr></table>`)
		case "/settings/instances/1/edit":
			fmt.Fprint(w, `<form><input name="url" value="http://sonarr:8989"><select name="type"><option value="sonarr" selected>Sonarr</option></select>`)
			for name, value := range map[string]string{"batch_size": "12", "sleep_interval_mins": "30", "hourly_cap": "12", "cooldown_days": "1", "post_release_grace_hrs": "6", "allowed_time_window": "06:00-00:00", "queue_limit": "2", "missing_hot_retry_window_hrs": "0", "tag_filter_include": "", "tag_filter_exclude": ""} {
				fmt.Fprintf(w, `<input name="%s" value="%s">`, name, value)
			}
			fmt.Fprintf(w, `<select name="sonarr_search_mode"><option value="%s" selected>Mode</option></select><select name="search_order"><option value="chronological" selected>Oldest</option></select><input type="checkbox" name="missing_enabled" checked><input type="checkbox" name="cutoff_enabled"><input type="checkbox" name="upgrade_enabled"></form>`, fixture.mode)
		case "/settings/instances/test-connection":
			w.Header().Set("HX-Trigger", "houndarr-connection-test-success")
			fmt.Fprint(w, "Connection successful")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fixture.hound.Close)
	t.Cleanup(fixture.cleanup.Close)
	return fixture
}

func fixturePort(t *testing.T, endpoint string) int {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestCompanionVerificationReportsPolicyFailuresWithoutMutatingServices(t *testing.T) {
	fixtures := newCompanionAPIFixture(t, 18080)
	seerrPreventSearch := true
	seerrAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/jellyfin":
			fmt.Fprint(w, `{"id":1}`)
		case "/api/v1/settings/sonarr":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"hostname": "sonarr", "port": 8989, "isDefault": true, "preventSearch": seerrPreventSearch}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer seerrAPI.Close()

	temporary := t.TempDir()
	declared := string(readFile(t, filepath.Join(repositoryRoot(t), "services/media/media-stack.yaml")))
	declared = strings.Replace(declared, "seerr: 15055", "seerr: "+strconv.Itoa(fixturePort(t, seerrAPI.URL)), 1)
	declared = strings.Replace(declared, "houndarr: 18877", "houndarr: "+strconv.Itoa(fixturePort(t, fixtures.hound.URL)), 1)
	declared = strings.Replace(declared, "cleanuparr: 21011", "cleanuparr: "+strconv.Itoa(fixturePort(t, fixtures.cleanup.URL)), 1)
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, []byte(declared), 0600)
	bin := filepath.Join(temporary, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "sops"), `#!/bin/sh
cat <<'YAML'
nordvpn:
  openvpn:
    serviceUsername: fixture-user
    servicePassword: fixture-password
profilarr:
  apiKey: fixture-profilarr-api-key-32-characters
jellyfin:
  username: household
  password: fixture-jellyfin-password
qbittorrent:
  username: household
  password: fixture-qbittorrent-password
houndarr:
  username: companion-admin
  password: fixture-houndarr-password
cleanuparr:
  apiKey: fixture-cleanuparr-key
YAML
`)
	writeExecutable(t, filepath.Join(bin, "docker"), `#!/bin/sh
cat >/dev/null
case "$*" in
 "compose -f - exec -T sonarr cat /config/config.xml") printf '<Config><ApiKey>fixture-sonarr-api-key</ApiKey></Config>' ;;
 *) exit 99 ;;
esac
`)
	for _, scenario := range []struct{ name, mode, code string }{{"wrong episode context", "season_context", "VERIFY_EPISODE_SEARCH_CONTEXT_INVALID"}, {"duplicate proactive owner", "episode", "VERIFY_DUPLICATE_PROACTIVE_OWNER"}} {
		t.Run(scenario.name, func(t *testing.T) {
			fixtures.mode = scenario.mode
			command := exec.Command("go", "run", "./cmd/media-stack", "verify", "--environment", "staging", "--config", configPath, "--suite", "companions", "--output", "json")
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), scenario.code) {
				t.Fatalf("expected %s: %v\n%s", scenario.code, err, output)
			}
			if fixtures.writes != 0 {
				t.Fatal("verify mutated policy")
			}
			if strings.Contains(string(output), "fixture-houndarr-password") || strings.Contains(string(output), "fixture-cleanuparr-key") {
				t.Fatal("verification exposed credentials")
			}
		})
	}

	t.Run("Seerr duplicate proactive owner", func(t *testing.T) {
		fixtures.mode = "episode"
		fixtures.converge()
		seerrPreventSearch = false
		command := exec.Command("go", "run", "./cmd/media-stack", "verify", "--environment", "staging", "--config", configPath, "--suite", "companions", "--output", "json")
		command.Dir = repositoryRoot(t)
		command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		seerrPreventSearch = true
		if err == nil || !strings.Contains(string(output), "VERIFY_REQUEST_ROUTING_FAILED") {
			t.Fatalf("Seerr ownership drift not detected: %v\n%s", err, output)
		}
	})
	t.Run("observed queue reasons", func(t *testing.T) {
		fixtures.mode = "episode"
		fixtures.converge()
		fixtures.events = []map[string]any{}
		for _, kind := range []string{"StalledStrike", "DownloadingMetadataStrike", "SlowSpeedStrike", "SlowTimeStrike", "FailedImportStrike"} {
			fixtures.events = append(fixtures.events, map[string]any{"eventType": kind, "strikeCount": 3, "timestamp": time.Now().UTC().Format(time.RFC3339), "itemHash": "legal-fixture-hash"})
		}
		command := exec.Command("go", "run", "./cmd/media-stack", "verify", "--environment", "staging", "--config", configPath, "--suite", "companions", "--output", "json")
		command.Dir = repositoryRoot(t)
		command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("verify: %v\n%s", err, output)
		}
		for _, code := range []string{"VERIFY_STALLED_DETECTED", "VERIFY_METADATA_STUCK_DETECTED", "VERIFY_SLOW_SPEED_DETECTED", "VERIFY_SLOW_COMPLETION_DETECTED", "VERIFY_FAILED_IMPORT_DETECTED", "VERIFY_SEARCH_CAP_EXHAUSTED"} {
			if !strings.Contains(string(output), code) {
				t.Errorf("missing %s: %s", code, output)
			}
		}
		if fixtures.writes != 0 {
			t.Fatal("verification mutated service settings")
		}
	})

	for _, scenario := range []struct {
		name, context, expect, code             string
		removed, blocked, replace, pack         bool
		threshold                               bool
		searchBeforeRemoval, fallbackBeforePack bool
		partialSearch                           bool
	}{
		{name: "below threshold", context: "episode", expect: "retained", code: "VERIFY_TRANSIENT_DOWNLOAD_RETAINED"},
		{name: "partial search below threshold", context: "episode", expect: "retained", code: "VERIFY_PREMATURE_CLEANUP", replace: true, partialSearch: true},
		{name: "season search below threshold", context: "season", expect: "retained", code: "VERIFY_PREMATURE_CLEANUP", replace: true},
		{name: "episode recovery", context: "episode", expect: "recovered", code: "VERIFY_SERIES_RECOVERY_PROVEN", removed: true, blocked: true, replace: true},
		{name: "season recovery", context: "season", expect: "recovered", code: "VERIFY_SEASON_REPLACEMENT_DISPATCHED", removed: true, blocked: true, replace: true, pack: true},
		{name: "blocklist failure", context: "episode", expect: "recovered", code: "VERIFY_BLOCKLIST_FAILED", removed: true, replace: true},
		{name: "blocklist operation rejected", context: "episode", expect: "recovered", code: "VERIFY_BLOCKLIST_FAILED", threshold: true},
		{name: "earlier proactive search", context: "episode", expect: "recovered", code: "VERIFY_REPLACEMENT_FAILED", removed: true, blocked: true, replace: true, searchBeforeRemoval: true},
		{name: "fallback before season replacement", context: "season", expect: "recovered", code: "VERIFY_REPLACEMENT_FAILED", removed: true, blocked: true, replace: true, pack: true, fallbackBeforePack: true},
		{name: "replacement failure", context: "episode", expect: "recovered", code: "VERIFY_REPLACEMENT_FAILED", removed: true, blocked: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixtures.converge()
			fixtures.mode = "episode"
			count := 3
			if scenario.removed || scenario.threshold {
				count = 12
			}
			fixtures.events = []map[string]any{{"eventType": "StalledStrike", "strikeCount": count, "timestamp": time.Now().UTC().Format(time.RFC3339), "itemHash": "legal-fixture-hash"}}
			if scenario.removed {
				fixtures.events = append(fixtures.events, map[string]any{"eventType": "QueueItemDeleted", "removeFromClient": true, "timestamp": time.Now().UTC().Format(time.RFC3339), "itemHash": "legal-fixture-hash"})
			}
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fixture", Path: "/"})
					fmt.Fprint(w, "Ok.")
				case "/api/v2/torrents/info":
					if scenario.removed {
						fmt.Fprint(w, "[]")
					} else {
						fmt.Fprint(w, `[{"hash":"legal-fixture-hash"}]`)
					}
				case "/api/v3/queue":
					if scenario.removed {
						fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
					} else {
						fmt.Fprint(w, `{"records":[{"downloadId":"legal-fixture-hash"}],"totalRecords":1}`)
					}
				case "/api/v3/blocklist":
					if scenario.blocked {
						fmt.Fprint(w, `{"records":[{"downloadId":"legal-fixture-hash"}],"totalRecords":1}`)
					} else {
						fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
					}

				case "/api/v3/command":
					commands := []map[string]any{}
					base := time.Now().UTC()
					if scenario.pack {
						commands = append(commands, map[string]any{"name": "SeasonSearch", "queued": base, "body": map[string]any{"seriesId": 42, "seasonNumber": 1}})
					}
					if scenario.replace {
						at := base.Add(time.Millisecond)
						if scenario.searchBeforeRemoval {
							at = base.Add(-10 * time.Second)
						}
						if scenario.fallbackBeforePack {
							at = base.Add(-time.Millisecond)
						}
						ids := []int{84, 85}
						if scenario.partialSearch {
							ids = []int{84}
						}
						commands = append(commands, map[string]any{"name": "EpisodeSearch", "queued": at, "body": map[string]any{"episodeIds": ids}})
					}
					_ = json.NewEncoder(w).Encode(commands)
				default:
					http.NotFound(w, r)
				}
			}))
			defer target.Close()
			port := fixturePort(t, target.URL)
			fixtures.documents["download_client"]["clients"].([]any)[0].(map[string]any)["host"] = "http://qbittorrent:" + strconv.Itoa(port)
			recoveryConfig := strings.Replace(declared, "qbittorrent: 18080", "qbittorrent: "+strconv.Itoa(port), 1)
			recoveryConfig = strings.Replace(recoveryConfig, "sonarr: 18989", "sonarr: "+strconv.Itoa(port), 1)
			recoveryConfigPath := filepath.Join(t.TempDir(), "media-stack.yaml")
			writeFile(t, recoveryConfigPath, []byte(recoveryConfig), 0600)
			fixturePath := filepath.Join(t.TempDir(), "recovery.yaml")
			contents := fmt.Sprintf(`apiVersion: homelab.media-stack/series-recovery/v1alpha1
kind: SeriesRecoveryFixture
spec:
  legalFixture: true
  downloadId: legal-fixture-hash
  seriesId: 42
  seasonNumber: 1
  episodeIds: [84, 85]
  replacementContext: %s
  reason: stalled
  observedAfter: %s
  timeout: 50ms
  expect: %s
`, scenario.context, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), scenario.expect)
			writeFile(t, fixturePath, []byte(contents), 0600)
			command := exec.Command("go", "run", "./cmd/media-stack", "verify", "--environment", "staging", "--config", recoveryConfigPath, "--suite", "companions", "--recovery-fixture", fixturePath, "--output", "json")
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if !strings.Contains(string(output), scenario.code) {
				t.Fatalf("expected %s: %v\n%s", scenario.code, err, output)
			}
			expectedFailure := scenario.code == "VERIFY_BLOCKLIST_FAILED" || scenario.code == "VERIFY_REPLACEMENT_FAILED" || scenario.code == "VERIFY_PREMATURE_CLEANUP"
			if (err != nil) != expectedFailure {
				t.Fatalf("unexpected exit: %v\n%s", err, output)
			}
		})
	}
}

func (fixture *companionAPIFixture) converge() {
	fixture.documents["sonarr"]["instances"] = []any{map[string]any{"id": "fixture-sonarr", "enabled": true, "name": "Sonarr", "url": "http://sonarr:8989", "version": float64(4), "externalUrl": nil}}
	fixture.documents["download_client"]["clients"] = []any{map[string]any{"id": "fixture-qbit", "enabled": true, "name": "qBittorrent", "typeName": "qBittorrent", "host": "http://qbittorrent:18080", "username": "household", "urlBase": nil, "externalUrl": nil, "downloadDirectorySource": nil, "downloadDirectoryTarget": nil}}
	fixture.documents["seeker"] = map[string]any{"searchEnabled": true, "searchInterval": float64(2), "proactiveSearchEnabled": false, "instances": []any{map[string]any{"arrInstanceId": "fixture-sonarr", "enabled": false}}}
	fixture.documents["queue_cleaner"] = map[string]any{"enabled": true, "cronExpression": "0 0/5 * * * ?", "useAdvancedScheduling": false, "downloadingMetadataMaxStrikes": float64(12), "processNoContentId": false, "ignoredDownloads": []any{}, "errored": map[string]any{"maxStrikes": float64(0)}, "failedImport": map[string]any{"maxStrikes": float64(12), "ignorePrivate": true, "deletePrivate": false, "skipIfNotFoundInClient": true, "patterns": []any{"No video files were found", "Not a valid video file"}, "patternMode": "Include", "changeCategory": false, "forceImport": false, "forceImportMaxTries": float64(3)}}
	for _, kind := range []string{"stall", "slow"} {
		rule := map[string]any{"id": "fixture-" + kind, "name": "Media Stack " + kind, "enabled": true, "maxStrikes": float64(12), "privacyType": "Public", "minCompletionPercentage": float64(0), "maxCompletionPercentage": float64(100), "deletePrivateTorrentsFromClient": false, "changeCategory": false, "resetStrikesOnProgress": true}
		if kind == "stall" {
			rule["minimumProgress"] = "1MB"
		} else {
			rule["minSpeed"] = "100KB"
			rule["maxTimeHours"] = float64(24)
			rule["ignoreAboveSize"] = nil
			rule["ignoreWhileAltSpeedActive"] = true
		}
		fixture.rules[kind] = []map[string]any{rule}
	}
}

func withoutSeriesAutomation(contents []byte) []byte {
	return regexp.MustCompile(`(?ms)^            seriesAutomation:\n.*?^            ports:\n`).ReplaceAll(contents, []byte("            ports:\n"))
}

func TestPromotionRequiresSeriesCompanionEvidenceAndStateCoverage(t *testing.T) {
	temporary := t.TempDir()
	configuration := readFile(t, filepath.Join(repositoryRoot(t), "services/media/media-stack.yaml"))
	versions := readFile(t, filepath.Join(repositoryRoot(t), "services/media/versions.yaml"))
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, configuration, 0600)
	digest := func(contents []byte) string { sum := sha256.Sum256(contents); return hex.EncodeToString(sum[:]) }
	verification := map[string]any{"schemaVersion": "homelab.media-stack/verify/v1alpha1", "environment": "staging", "suite": "promotion", "configDigest": digest(configuration), "versionsDigest": digest(versions), "diagnostics": []any{}}
	backup := map[string]any{"schemaVersion": "homelab.media-stack/backup/v1alpha1", "environment": "staging", "services": []any{}}
	verificationPath, backupPath := filepath.Join(temporary, "verification.json"), filepath.Join(temporary, "backup.json")
	run := func() ([]byte, error) {
		t.Helper()
		for path, value := range map[string]any{verificationPath: verification, backupPath: backup} {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, encoded, 0600)
		}
		command := exec.Command("go", "run", "./cmd/media-stack", "promote", "--environment", "production", "--config", configPath, "--verification", verificationPath, "--backup", backupPath)
		command.Dir = repositoryRoot(t)
		return command.CombinedOutput()
	}
	output, err := run()
	if err == nil || !strings.Contains(string(output), "companion") {
		t.Fatalf("promotion accepted absent companion proof: %v\n%s", err, output)
	}
	diagnostics := []any{}
	for _, code := range []string{"VERIFY_COMPANION_POLICY_CONVERGED", "VERIFY_EPISODE_SEARCH_DISPATCHED", "VERIFY_SERIES_RECOVERY_PROVEN", "VERIFY_SERIES_REQUESTED", "VERIFY_SERIES_EPISODE_ACQUIRED", "VERIFY_SERIES_EPISODE_HARDLINKED", "VERIFY_SERIES_EPISODE_PLAYBACK_READY"} {
		diagnostics = append(diagnostics, map[string]any{"code": code, "status": "pass"})
	}
	verification["diagnostics"] = diagnostics
	output, err = run()
	if err == nil || !strings.Contains(string(output), "companion state") {
		t.Fatalf("promotion accepted absent companion backup: %v\n%s", err, output)
	}
	backup["services"] = []any{map[string]any{"name": "houndarr", "volume": "houndarr-config", "dockerVolume": "media-staging_houndarr-config"}, map[string]any{"name": "cleanuparr", "volume": "cleanuparr-config", "dockerVolume": "media-staging_cleanuparr-config"}}
	output, err = run()
	if err != nil {
		t.Fatalf("promotion rejected matching companion proof: %v\n%s", err, output)
	}
}

func TestPlanRejectsSharedCompanionIdentityAndPorts(t *testing.T) {
	original := string(readFile(t, filepath.Join(repositoryRoot(t), "services/media/media-stack.yaml")))
	for _, scenario := range []struct{ name, old, new, diagnostic string }{
		{"project", "projectName: media-staging", "projectName: media-production", "companion state"},
		{"secrets", "secretsFile: secrets/staging.sops.yaml", "secretsFile: secrets/production.sops.yaml", "companion state"},
		{"port", "houndarr: 18877", "houndarr: 8877", "companion port collision"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "media-stack.yaml")
			writeFile(t, path, []byte(strings.Replace(original, scenario.old, scenario.new, 1)), 0600)
			command := exec.Command("go", "run", "./cmd/media-stack", "plan", "--environment", "staging", "--config", path)
			command.Dir = repositoryRoot(t)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), scenario.diagnostic) {
				t.Fatalf("accepted shared %s: %v\n%s", scenario.name, err, output)
			}
		})
	}
}
