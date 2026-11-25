package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/sonarr"
	"gopkg.in/yaml.v3"
)

type refreshArr struct {
	address, key, kind, fileResource string
	http                             *http.Client
}

type refreshArrFile struct {
	ID   int
	Path string
}

type refreshArrConnector interface {
	Ready(context.Context) error
	ReconcileJellyfinConnection(context.Context, string) error
}

type refreshArrImport struct {
	source      string
	titleFields map[string]any
}

func exerciseNativeArrRefresh(t *testing.T, server *refreshJellyfin, image, project, service, port string, contents []byte) {
	t.Helper()
	data := filepath.Dir(server.media)
	incoming := filepath.Join(data, "incoming")
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		t.Fatal(err)
	}
	arr := refreshArr{kind: "movie", fileResource: "moviefile", http: &http.Client{Timeout: 40 * time.Second}}
	root := "/data/media/movies"
	namingToggle, namingFormat, format := "renameMovies", "standardMovieFormat", "{Movie CleanTitle} ({Release Year}) [{Quality Title}]"
	newConnector := func(address, key string) refreshArrConnector { return radarr.New(address, key, arr.http) }
	importSources := func(id int) []refreshArrImport {
		return []refreshArrImport{{source: "Night.of.the.Living.Dead.1968", titleFields: map[string]any{"movieId": id}}}
	}
	if service == "sonarr" {
		arr.kind, arr.fileResource = "series", "episodefile"
		root = "/data/media/series"
		namingToggle, namingFormat, format = "renameEpisodes", "standardEpisodeFormat", "{Series Title} - S{season:00}E{episode:00} [{Quality Title}]"
		newConnector = func(address, key string) refreshArrConnector { return sonarr.New(address, key, arr.http) }
		importSources = func(id int) []refreshArrImport { return arr.seasonImports(t, id) }
	}
	arr.address, arr.key = startRefreshArr(t, image, project, service, port, data, func(ctx context.Context, address, key string) error {
		return newConnector(address, key).Ready(ctx)
	})
	connector := newConnector(arr.address, arr.key)
	for range 2 {
		if err := connector.ReconcileJellyfinConnection(context.Background(), server.key); err != nil {
			t.Fatalf("reconcile pinned connector: %v", err)
		}
	}
	var naming map[string]any
	arr.request(t, http.MethodGet, "/config/naming", nil, &naming)
	naming[namingToggle], naming[namingFormat] = true, format
	namingPath := fmt.Sprintf("/config/naming/%v", naming["id"])
	arr.request(t, http.MethodPut, namingPath, naming, nil)
	id := arr.addTitle(t, root, arr.fixtureLookup(t))
	sources := importSources(id)
	scan := server.scan(t)

	check := func(operation string, completed time.Time, current []refreshArrFile, stale ...refreshArrFile) {
		t.Helper()
		var present, absent []string
		for _, file := range current {
			relative := strings.TrimPrefix(file.Path, "/data/media/")
			if relative == file.Path {
				t.Fatalf("%s returned unexpected library path %q", operation, file.Path)
			}
			present = append(present, relative)
		}
		for _, file := range stale {
			absent = append(absent, strings.TrimPrefix(file.Path, "/data/media/"))
		}
		server.waitPathsSince(t, operation, completed, present, absent, &scan)
	}

	importFiles := func(batch []refreshArrImport, qualityID int, quality string) time.Time {
		t.Helper()
		var files []map[string]any
		for _, source := range batch {
			name := source.source + "." + quality + ".mp4"
			if err := os.WriteFile(filepath.Join(incoming, name), contents, 0o640); err != nil {
				t.Fatal(err)
			}
			file := map[string]any{
				"path": "/data/incoming/" + name,
				"quality": map[string]any{
					"quality":  map[string]int{"id": qualityID},
					"revision": map[string]int{"version": 1},
				},
				"languages": []map[string]int{{"id": 1}},
			}
			for field, value := range source.titleFields {
				file[field] = value
			}
			files = append(files, file)
		}
		return arr.command(t, map[string]any{"name": "ManualImport", "importMode": "Move", "files": files})
	}
	completed := importFiles(sources, 4, "720p.HDTV")
	imported := arr.files(t, id, len(sources))
	check("index the native import", completed, imported)

	completed = importFiles(sources[:1], 9, "1080p.HDTV")
	upgraded := arr.files(t, id, len(sources))
	var replaced []refreshArrFile
	for _, file := range imported {
		if !slices.Contains(upgraded, file) {
			replaced = append(replaced, file)
		}
	}
	if len(replaced) != 1 {
		t.Fatalf("Arr replaced %d files during a single-item upgrade, want one", len(replaced))
	}
	check("index the native upgrade", completed, upgraded, replaced...)

	naming[namingFormat] = format + " - Renamed"
	arr.request(t, http.MethodPut, namingPath, naming, nil)
	var ids []int
	for _, file := range upgraded {
		ids = append(ids, file.ID)
	}
	completed = arr.command(t, map[string]any{"name": "RenameFiles", arr.kind + "Id": id, "files": ids})
	renamed := arr.files(t, id, len(sources))
	check("index the native rename", completed, renamed, upgraded...)

	arr.request(t, http.MethodDelete, fmt.Sprintf("/%s/%d", arr.fileResource, renamed[0].ID), nil, nil)
	check("remove the native file deletion", waitForArrFileRemoval(t, server, renamed[:1]), renamed[1:], renamed[0])
	if len(renamed) == 1 {
		completed = importFiles(sources, 4, "720p.HDTV")
		renamed = arr.files(t, id, 1)
		check("index the movie before title deletion", completed, renamed)
	} else {
		renamed = renamed[1:]
	}
	arr.request(t, http.MethodDelete, fmt.Sprintf("/%s/%d?deleteFiles=true", arr.kind, id), nil, nil)
	check("remove the native title deletion", waitForArrFileRemoval(t, server, renamed), nil, renamed...)
}

func waitForArrFileRemoval(t *testing.T, server *refreshJellyfin, files []refreshArrFile) time.Time {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var remaining []string
		for _, file := range files {
			path := strings.TrimPrefix(file.Path, "/data/media/")
			if path == file.Path {
				t.Fatalf("Arr file path %q is outside the disposable libraries", file.Path)
			}
			if _, err := os.Stat(server.file(path)); err == nil {
				remaining = append(remaining, path)
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		observed := time.Now()
		if len(remaining) == 0 {
			return observed
		}
		if !observed.Before(deadline) {
			t.Fatalf("Arr did not remove files from disk: %v", remaining)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (arr refreshArr) fixtureLookup(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "services", "media", "fixtures", "legal-"+arr.kind+".yaml")
	var fixture struct {
		Spec struct {
			TMDBID int `yaml:"tmdbId"`
			TVDBID int `yaml:"tvdbId"`
		}
	}
	if err := yaml.Unmarshal(readFile(t, path), &fixture); err != nil {
		t.Fatal(err)
	}
	if arr.kind == "series" {
		return fmt.Sprintf("tvdb:%d", fixture.Spec.TVDBID)
	}
	return fmt.Sprintf("tmdb:%d", fixture.Spec.TMDBID)
}

func (arr refreshArr) request(t *testing.T, method, path string, input, output any) {
	t.Helper()
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, arr.address+"/api/v3"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", arr.key)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := arr.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		t.Fatalf("%s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.ReplaceAll(string(detail), arr.key, "[redacted]"))
	}
	if output != nil {
		if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
}

func (arr refreshArr) addTitle(t *testing.T, root, lookup string) int {
	t.Helper()
	arr.request(t, http.MethodPost, "/rootfolder", map[string]string{"path": root}, nil)
	var profiles []struct{ ID int }
	arr.request(t, http.MethodGet, "/qualityprofile", nil, &profiles)
	if len(profiles) == 0 {
		t.Fatal("pinned Arr did not initialize a quality profile")
	}
	var titles []map[string]any
	arr.request(t, http.MethodGet, "/"+arr.kind+"/lookup?term="+url.QueryEscape(lookup), nil, &titles)
	if len(titles) != 1 {
		t.Fatalf("metadata lookup %s yielded %d titles", lookup, len(titles))
	}
	title := titles[0]
	title["qualityProfileId"], title["rootFolderPath"], title["monitored"] = profiles[0].ID, root, true
	title["seasonFolder"] = true
	var added struct{ ID int }
	arr.request(t, http.MethodPost, "/"+arr.kind, title, &added)
	if added.ID <= 0 {
		t.Fatal("Arr did not return a title ID")
	}
	return added.ID
}

type refreshArrEpisode struct {
	ID, SeasonNumber, EpisodeNumber int
}

func (arr refreshArr) seasonImports(t *testing.T, id int) []refreshArrImport {
	t.Helper()
	var episodes []refreshArrEpisode
	pollJellyfin(t, time.Now().Add(time.Minute), "load all 30 episodes of The Lucy Show season one", func() (bool, error) {
		var items []refreshArrEpisode
		arr.request(t, http.MethodGet, fmt.Sprintf("/episode?seriesId=%d", id), nil, &items)
		episodes = slices.DeleteFunc(items, func(item refreshArrEpisode) bool { return item.SeasonNumber != 1 })
		if len(episodes) != 30 {
			return false, fmt.Errorf("Sonarr listed %d episodes in season one, want 30", len(episodes))
		}
		return true, nil
	})
	var sources []refreshArrImport
	for _, episode := range episodes {
		sources = append(sources, refreshArrImport{
			source:      fmt.Sprintf("The.Lucy.Show.S01E%02d", episode.EpisodeNumber),
			titleFields: map[string]any{"seriesId": id, "episodeIds": []int{episode.ID}},
		})
	}
	return sources
}

func (arr refreshArr) command(t *testing.T, command map[string]any) time.Time {
	t.Helper()
	var queued struct{ ID int }
	arr.request(t, http.MethodPost, "/command", command, &queued)
	var observed struct {
		Status, Result, Message string
		Ended                   time.Time
	}
	pollJellyfin(t, time.Now().Add(time.Minute), fmt.Sprintf("complete the Arr %s command", command["name"]), func() (bool, error) {
		arr.request(t, http.MethodGet, fmt.Sprintf("/command/%d", queued.ID), nil, &observed)
		switch observed.Status {
		case "completed":
			if observed.Result != "successful" || observed.Ended.IsZero() {
				t.Fatalf("Arr %s command did not succeed: %+v", command["name"], observed)
			}
			return true, nil
		case "failed", "aborted":
			t.Fatalf("Arr %s command failed: %+v", command["name"], observed)
		}
		return false, fmt.Errorf("status %s", observed.Status)
	})
	return observed.Ended
}

func (arr refreshArr) files(t *testing.T, id, expected int) []refreshArrFile {
	t.Helper()
	var files []refreshArrFile
	arr.request(t, http.MethodGet, fmt.Sprintf("/%s?%sId=%d", arr.fileResource, arr.kind, id), nil, &files)
	if len(files) != expected {
		t.Fatalf("Arr listed %d imported files, want %d: %v", len(files), expected, files)
	}
	return files
}
