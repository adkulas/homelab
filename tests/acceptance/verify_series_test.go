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
	"sync"
	"testing"
)

func TestPromotionVerifyAcquiresAndHardlinkImportsLegalSeriesEpisode(t *testing.T) {
	temporary := t.TempDir()
	dataRoot := filepath.Join(temporary, "data")
	sourcePath := filepath.Join(dataRoot, "torrents", "series", "The.Lucy.Show.S01E01.mp4")
	importPath := filepath.Join(dataRoot, "media", "series", "The Lucy Show", "Season 01", "The Lucy Show - S01E01.mp4")
	for _, directory := range []string{filepath.Dir(sourcePath), filepath.Dir(importPath)} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	var mutex sync.Mutex
	seriesAdded, releaseGrabbed, imported := false, false, false
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if strings.HasPrefix(request.URL.Path, "/api/v3/") {
			if request.Header.Get("X-Api-Key") != "fixture-sonarr-api-key" {
				http.Error(writer, "Unauthorized", http.StatusUnauthorized)
				return
			}
			switch request.Method + " " + request.URL.Path {
			case "GET /api/v3/series":
				if seriesAdded {
					_, _ = io.WriteString(writer, `[{"id":42,"title":"The Lucy Show","tvdbId":103354}]`)
				} else {
					_, _ = io.WriteString(writer, `[]`)
				}
			case "GET /api/v3/series/lookup":
				if request.URL.Query().Get("term") != "tvdb:103354" {
					http.Error(writer, "unexpected lookup", http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(writer, `[{"title":"The Lucy Show","tvdbId":103354,"seasons":[{"seasonNumber":1}]}]`)
			case "GET /api/v3/qualityprofile":
				_, _ = io.WriteString(writer, `[{"id":7,"name":"Fixture 1080p"}]`)
			case "POST /api/v3/series":
				var series map[string]any
				_ = json.NewDecoder(request.Body).Decode(&series)
				if series["tvdbId"] != float64(103354) || series["rootFolderPath"] != "/data/media/series" || series["qualityProfileId"] != float64(7) {
					http.Error(writer, "unexpected series declaration", http.StatusBadRequest)
					return
				}
				seriesAdded = true
				series["id"] = 42
				_ = json.NewEncoder(writer).Encode(series)
			case "GET /api/v3/episode":
				_, _ = io.WriteString(writer, `[{"id":84,"seriesId":42,"seasonNumber":1,"episodeNumber":1}]`)
			case "GET /api/v3/release":
				if request.URL.Query().Get("episodeId") != "84" {
					http.Error(writer, "unexpected episode", http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(writer, `[{"guid":"fixture-release","title":"The Lucy Show S01E01","indexer":"Internet Archive","episodeId":84}]`)
			case "POST /api/v3/release":
				if err := os.WriteFile(sourcePath, []byte("public-domain fixture episode\n"), 0o640); err != nil {
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}
				releaseGrabbed = true
			case "GET /api/v3/episodefile":
				if !releaseGrabbed {
					_, _ = io.WriteString(writer, `[]`)
					return
				}
				if !imported {
					if err := os.Link(sourcePath, importPath); err != nil {
						http.Error(writer, err.Error(), http.StatusInternalServerError)
						return
					}
					imported = true
				}
				_, _ = io.WriteString(writer, `[{"id":73,"seriesId":42,"path":"/data/media/series/The Lucy Show/Season 01/The Lucy Show - S01E01.mp4","size":30}]`)
			default:
				http.NotFound(writer, request)
			}
			return
		}
		if request.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(writer, &http.Cookie{Name: "SID", Value: "fixture-session", Path: "/"})
			_, _ = io.WriteString(writer, "Ok.")
			return
		}
		if cookie, err := request.Cookie("SID"); err != nil || cookie.Value != "fixture-session" {
			http.Error(writer, "Forbidden", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/api/v2/torrents/info":
			if releaseGrabbed {
				_, _ = io.WriteString(writer, `[{"hash":"fixture-hash","name":"The Lucy Show S01E01","category":"series","progress":1,"save_path":"/data/torrents/series"}]`)
			} else {
				_, _ = io.WriteString(writer, `[]`)
			}
		case "/api/v2/torrents/files":
			_, _ = io.WriteString(writer, `[{"name":"The.Lucy.Show.S01E01.mp4","size":30,"progress":1}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()
	apiURL, err := url.Parse(api.URL)
	if err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(temporary, "media-stack.yaml")
	declared := string(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")))
	declared = strings.Replace(declared, "dataRoot: /srv/media/staging", "dataRoot: "+dataRoot, 1)
	declared = strings.Replace(declared, "qbittorrent: 18080", "qbittorrent: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "sonarr: 18989", "sonarr: "+apiURL.Port(), 1)
	writeFile(t, configPath, []byte(declared), 0o600)
	fixturePath := filepath.Join(temporary, "legal-series.yaml")
	writeFile(t, fixturePath, []byte(`apiVersion: homelab.media-stack/legal-series/v1alpha1
kind: LegalSeriesFixture
spec:
  title: The Lucy Show
  tvdbId: 103354
  seasonNumber: 1
  episodeNumber: 1
  releaseTitle: The Lucy Show S01E01
  indexer: Internet Archive
  timeout: 5s
`), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "curl"), []byte("#!/bin/sh\nprintf '198.51.100.10\\n'\n"), 0o700)
	writeFile(t, filepath.Join(binDirectory, "docker"), []byte(`#!/bin/sh
cat >/dev/null
case "$*" in
  "run --rm --device /dev/net/tun --entrypoint /bin/sh "*) exit 0 ;;
  "compose -f - ps --format json gluetun") printf '{"Health":"healthy","State":"running"}\n'; exit 0 ;;
  "compose -f - exec -T qbittorrent curl --ipv4 --fail --silent --show-error --max-time 10 "*)
    count=0
    [ -f "$VERIFY_PROBE_COUNT" ] && count=$(cat "$VERIFY_PROBE_COUNT")
    count=$((count + 1))
    printf '%s' "$count" > "$VERIFY_PROBE_COUNT"
    [ "$count" -eq 2 ] && exit 28
    printf '198.51.100.20\n'; exit 0 ;;
  "compose -f - stop gluetun") exit 0 ;;
  "compose -f - start gluetun") exit 0 ;;
  "compose -f - up -d --force-recreate qbittorrent") exit 0 ;;
  "compose -f - logs --no-color qbittorrent") printf 'A temporary password is provided for this session: fixture-temporary-password\n'; exit 0 ;;
  "compose -f - exec -T sonarr cat /config/config.xml") printf '<Config><ApiKey>fixture-sonarr-api-key</ApiKey></Config>\n'; exit 0 ;;
  *) exit 99 ;;
esac
`), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "verify",
		"--environment", "staging", "--config", configPath, "--suite", "promotion",
		"--legal-series-fixture", fixturePath, "--output", "json")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERIFY_PROBE_COUNT="+filepath.Join(temporary, "probe-count"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack promotion verification failed: %v\n%s", err, output)
	}
	var report struct {
		Diagnostics []struct {
			Code   string `json:"code"`
			Status string `json:"status"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode verify report: %v\n%s", err, output)
	}
	passed := map[string]bool{}
	for _, diagnostic := range report.Diagnostics {
		passed[diagnostic.Code] = diagnostic.Status == "pass"
	}
	for _, code := range []string{"VERIFY_SERIES_EPISODE_ACQUIRED", "VERIFY_SERIES_EPISODE_HARDLINKED"} {
		if !passed[code] {
			t.Errorf("promotion verification did not report %s: %s", code, output)
		}
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	importInfo, err := os.Stat(importPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, importInfo) {
		t.Fatal("Series Library import does not share inode identity with qBittorrent source")
	}
}
