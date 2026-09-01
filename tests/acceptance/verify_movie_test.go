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
	"time"
)

func TestPromotionVerifyRequestsAndPlaysLegalMovieThroughSeerr(t *testing.T) {
	temporary := t.TempDir()
	dataRoot := filepath.Join(temporary, "data")
	sourcePath := filepath.Join(dataRoot, "torrents", "movies", "Night.of.the.Living.Dead.1968.mp4")
	importPath := filepath.Join(dataRoot, "media", "movies", "Night of the Living Dead (1968)", "Night of the Living Dead (1968).mp4")
	for _, directory := range []string{filepath.Dir(sourcePath), filepath.Dir(importPath)} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	var mutex sync.Mutex
	movieAdded, movieRequested, releaseGrabbed, imported := false, false, false, false
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if strings.HasPrefix(request.URL.Path, "/api/v3/") {
			if request.Header.Get("X-Api-Key") != "fixture-radarr-api-key" {
				http.Error(writer, "Unauthorized", http.StatusUnauthorized)
				return
			}
			switch request.Method + " " + request.URL.Path {
			case "GET /api/v3/movie":
				if movieAdded {
					_, _ = io.WriteString(writer, `[{"id":41,"title":"Night of the Living Dead","tmdbId":10331,"path":"/data/media/movies/Night of the Living Dead (1968)"}]`)
				} else {
					_, _ = io.WriteString(writer, `[]`)
				}
			case "GET /api/v3/moviefile":
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
				_, _ = io.WriteString(writer, `[{"id":73,"movieId":41,"path":"/data/media/movies/Night of the Living Dead (1968)/Night of the Living Dead (1968).mp4","size":28}]`)
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
				_, _ = io.WriteString(writer, `[{"hash":"fixture-hash","name":"Night Of The Living Dead 1968","category":"movies","progress":1,"save_path":"/data/torrents/movies"}]`)
			} else {
				_, _ = io.WriteString(writer, `[]`)
			}
		case "/api/v2/torrents/files":
			_, _ = io.WriteString(writer, `[{"name":"Night.of.the.Living.Dead.1968.mp4","size":28,"progress":1}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()
	seerrAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "POST /api/v1/auth/jellyfin":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["username"] != "household" || body["password"] != "fixture-jellyfin-password" {
				http.Error(writer, "unexpected household authentication", http.StatusUnauthorized)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "connect.sid", Value: "request-session", Path: "/"})
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": 1, "email": "household", "permissions": 2})
		case "POST /api/v1/request":
			if cookie, err := request.Cookie("connect.sid"); err != nil || cookie.Value != "request-session" {
				http.Error(writer, "missing household session", http.StatusUnauthorized)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["mediaType"] != "movie" || body["mediaId"] != float64(10331) || body["is4k"] != false {
				http.Error(writer, "unexpected movie request", http.StatusBadRequest)
				return
			}
			movieRequested = true
			go func() {
				time.Sleep(100 * time.Millisecond)
				mutex.Lock()
				defer mutex.Unlock()
				if err := os.WriteFile(sourcePath, []byte("public-domain fixture movie\n"), 0o640); err != nil {
					return
				}
				movieAdded = true
				releaseGrabbed = true
			}()
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": 9, "status": 2, "type": "movie"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer seerrAPI.Close()
	jellyfinAPI := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "POST /Users/AuthenticateByName":
			_ = json.NewEncoder(writer).Encode(map[string]any{"AccessToken": "fixture-jellyfin-token", "User": map[string]any{"Id": "jellyfin-user", "Policy": map[string]any{}}})
		case "GET /Users/jellyfin-user/Items":
			items := []any{}
			if imported {
				items = append(items, map[string]any{"Id": "movie-1", "Name": "Night of the Living Dead", "Path": "/data/media/movies/Night of the Living Dead (1968)/Night of the Living Dead (1968).mp4"})
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"Items": items})
		case "GET /Items/movie-1/PlaybackInfo":
			_ = json.NewEncoder(writer).Encode(map[string]any{"MediaSources": []any{map[string]any{"Path": "/data/media/movies/Night of the Living Dead (1968)/Night of the Living Dead (1968).mp4", "SupportsDirectPlay": true}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer jellyfinAPI.Close()
	jellyfinURL, err := url.Parse(jellyfinAPI.URL)
	if err != nil {
		t.Fatal(err)
	}
	apiURL, err := url.Parse(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	seerrURL, err := url.Parse(seerrAPI.URL)
	if err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(temporary, "media-stack.yaml")
	declared := string(readFile(t, filepath.Join(repositoryRoot(t), "stacks", "media", "media-stack.yaml")))
	declared = strings.Replace(declared, "dataRoot: /srv/media/staging", "dataRoot: "+dataRoot, 1)
	declared = strings.Replace(declared, "qbittorrent: 18080", "qbittorrent: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "radarr: 17878", "radarr: "+apiURL.Port(), 1)
	declared = strings.Replace(declared, "jellyfin: 18096", "jellyfin: "+jellyfinURL.Port(), 1)
	declared = strings.Replace(declared, "seerr: 15055", "seerr: "+seerrURL.Port(), 1)
	writeFile(t, configPath, []byte(declared), 0o600)
	if err := os.Mkdir(filepath.Join(temporary, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temporary, "secrets", "staging.sops.yaml"), []byte("encrypted: true\n"), 0o600)
	fixturePath := filepath.Join(temporary, "legal-movie.yaml")
	writeFile(t, fixturePath, []byte(`apiVersion: homelab.media-stack/legal-movie/v1alpha1
kind: LegalMovieFixture
spec:
  title: Night of the Living Dead
  tmdbId: 10331
  releaseTitle: Night Of The Living Dead 1968
  indexer: Internet Archive
  timeout: 5s
`), 0o600)

	binDirectory := filepath.Join(temporary, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(binDirectory, "sops"), []byte("#!/bin/sh\nprintf 'nordvpn:\n  openvpn:\n    serviceUsername: fixture-user\n    servicePassword: fixture-password\nprofilarr:\n  apiKey: fixture-profilarr-api-key-32-characters\njellyfin:\n  username: household\n  password: fixture-jellyfin-password\nqbittorrent:\n  username: household\n  password: fixture-qbittorrent-password\n'\n"), 0o700)
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
  "run --rm --no-healthcheck -i --network media-staging_application --entrypoint /bin/sh "*) exit 0 ;;
  "compose -f - logs --no-color qbittorrent") printf 'A temporary password is provided for this session: fixture-temporary-password\n'; exit 0 ;;
  "compose -f - exec -T radarr cat /config/config.xml") printf '<Config><ApiKey>fixture-radarr-api-key</ApiKey></Config>\n'; exit 0 ;;
  *) exit 99 ;;
esac
`), 0o700)

	command := exec.Command("go", "run", "./cmd/media-stack", "verify",
		"--environment", "staging", "--config", configPath, "--suite", "promotion",
		"--legal-fixture", fixturePath, "--output", "json")
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
	for _, code := range []string{"VERIFY_MOVIE_REQUESTED", "VERIFY_MOVIE_ACQUIRED", "VERIFY_MOVIE_HARDLINKED", "VERIFY_MOVIE_PLAYBACK_READY"} {
		if !passed[code] {
			t.Errorf("promotion verification did not report %s: %s", code, output)
		}
	}
	if !movieRequested {
		t.Fatal("promotion verification bypassed the approved Seerr movie request")
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
		t.Fatal("Movie Library import does not share inode identity with qBittorrent source")
	}
}
