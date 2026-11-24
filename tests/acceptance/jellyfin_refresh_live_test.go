package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/sonarr"
)

func TestDisposablePinnedJellyfinLibraryRefresh(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_JELLYFIN_REFRESH") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_JELLYFIN_REFRESH=1 to exercise Jellyfin refresh on disposable pinned images")
	}
	versions, err := config.LoadVersions(filepath.Join(repositoryRoot(t), "services", "media", "versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("media-stack-refresh-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	refreshDocker(t, "network", "create", project)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", project).Run() })
	ctx := context.Background()
	staging := startRefreshJellyfin(t, versions.Images["jellyfin"], project, "staging", "jellyfin")
	production := startRefreshJellyfin(t, versions.Images["jellyfin"], project, "production", "production-jellyfin")
	if staging.key == production.key {
		t.Fatal("disposable Environments share a Jellyfin refresh credential")
	}
	staging.configureScan(t, (localClockMinutes(time.Now().UTC())+240)%1440)
	production.configureScan(t, (localClockMinutes(time.Now().UTC())+180)%1440)
	productionBaseline := production.scan(t)

	for _, service := range []struct {
		name    string
		port    string
		connect func(string, string) error
	}{
		{"radarr", "7878", func(address, key string) error {
			return radarr.New(address, key, staging.http).ReconcileJellyfinConnection(ctx, staging.key)
		}},
		{"sonarr", "8989", func(address, key string) error {
			return sonarr.New(address, key, staging.http).ReconcileJellyfinConnection(ctx, staging.key)
		}},
	} {
		address, key := startRefreshArr(t, versions.Images[service.name], project, service.name, service.port)
		for range 2 {
			if err := service.connect(address, key); err != nil {
				t.Fatalf("reconcile pinned %s connector: %v", service.name, err)
			}
		}
	}

	fixture := filepath.Join(t.TempDir(), "fixture.mp4")
	refreshDocker(t, "run", "--rm", "--pull", "never", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--entrypoint", "/usr/lib/jellyfin-ffmpeg/ffmpeg", "--volume", filepath.Dir(fixture)+":/fixture",
		versions.Images["jellyfin"], "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "/fixture/fixture.mp4")
	contents, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, library := range []struct {
		name             string
		initial, renamed []string
	}{
		{"Movie Library",
			[]string{"movies/Refresh Test Movie (2024)/Refresh Test Movie (2024).mp4"},
			[]string{"movies/Refresh Test Movie (2024)/Refresh Test Movie (2024) [1080p].mp4"}},
		{"Series Library",
			[]string{"series/Refresh Test Show/Season 01/Refresh Test Show - S01E01.mp4", "series/Refresh Test Show/Season 01/Refresh Test Show - S01E02.mp4"},
			[]string{"series/Refresh Test Show/Season 01/Refresh Test Show - S01E01 - Updated.mp4", "series/Refresh Test Show/Season 01/Refresh Test Show - S01E02 - Updated.mp4"}},
	} {
		t.Run(library.name, func(t *testing.T) {
			baseline := staging.scan(t)
			for _, file := range library.initial {
				staging.publish(t, file, contents)
			}
			staging.waitPaths(t, "discover additions", library.initial, nil, &baseline)
			for i, old := range library.initial {
				if err := os.Rename(staging.file(old), staging.file(library.renamed[i])); err != nil {
					t.Fatal(err)
				}
			}
			staging.waitPaths(t, "reflect renames and clear stale paths", library.renamed, library.initial, &baseline)
			for _, file := range library.renamed {
				if err := os.Remove(staging.file(file)); err != nil {
					t.Fatal(err)
				}
			}
			staging.waitPaths(t, "remove deleted paths", nil, library.renamed, &baseline)
		})
	}

	t.Run("recovery scans", func(t *testing.T) {
		missed := []string{"movies/Recovery Movie (2024)/Recovery Movie (2024).mp4", "series/Recovery Show/Season 01/Recovery Show - S01E01.mp4"}
		baseline := staging.scan(t)
		refreshDocker(t, "stop", staging.container)
		for _, file := range missed {
			staging.publish(t, file, contents)
		}
		refreshDocker(t, "start", staging.container)
		staging.refreshAddress(t)
		pollJellyfin(t, time.Now().Add(time.Minute), "restart after missing filesystem changes", func() (bool, error) {
			return true, staging.client.Ready(ctx)
		})
		paths, err := staging.paths()
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range missed {
			if slices.Contains(paths, path) {
				t.Fatalf("missed change %s was already visible before the recovery scan", path)
			}
		}
		recoveryTime := time.Now().UTC().Add(5 * time.Second).Truncate(time.Minute).Add(time.Minute)
		staging.configureScan(t, localClockMinutes(recoveryTime))
		staging.waitPaths(t, "recover missed changes through the scheduled scan", missed, nil, nil)
		var recovered refreshScan
		pollJellyfin(t, time.Now().Add(time.Minute), "record a successful scheduled scan", func() (bool, error) {
			recovered = staging.scan(t)
			return recovered.Result.End.After(baseline.Result.End) && recovered.Result.Status == "Completed", nil
		})
		staging.configureScan(t, (localClockMinutes(time.Now().UTC())+240)%1440)
		if err := staging.request(http.MethodPost, "/ScheduledTasks/Running/"+recovered.ID, nil); err != nil {
			t.Fatal(err)
		}
		pollJellyfin(t, time.Now().Add(2*time.Minute), "finish a manual Staging scan", func() (bool, error) {
			current := staging.scan(t)
			return current.Result.End.After(recovered.Result.End) && current.Result.Status == "Completed", nil
		})
		if after := production.scan(t); after.Result != productionBaseline.Result || !bytes.Equal(after.Triggers, productionBaseline.Triggers) {
			t.Fatal("the isolated Production recovery schedule or scan history changed during Staging verification")
		}
	})
}

func localClockMinutes(value time.Time) int { return value.Hour()*60 + value.Minute() }

type refreshJellyfin struct {
	container, address, media, key string
	userID                         string
	credentials                    jellyfin.Credentials
	client                         *jellyfin.Client
	http                           *http.Client
}

func startRefreshJellyfin(t *testing.T, image, project, environment, alias string) *refreshJellyfin {
	t.Helper()
	root := t.TempDir()
	fixture := &refreshJellyfin{
		container:   project + "-" + environment,
		media:       filepath.Join(root, "media"),
		credentials: jellyfin.Credentials{Username: "refresh-test-admin", Password: "disposable-refresh-test-password"},
		http:        &http.Client{Timeout: 10 * time.Second},
	}
	for _, dir := range []string{"config", "cache", "media/movies", "media/series"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, library := range []string{"movies", "series"} {
		if err := os.WriteFile(fixture.file(library+"/media-stack-library.keep"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refreshDocker(t, "run", "--detach", "--pull", "never", "--name", fixture.container,
		"--network", project, "--network-alias", alias, "--env", "TZ=UTC",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--publish", "127.0.0.1::8096",
		"--volume", filepath.Join(root, "config")+":/config", "--volume", filepath.Join(root, "cache")+":/cache",
		"--volume", fixture.media+":/data/media:ro", image)
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", "--tail", "80", fixture.container).CombinedOutput()
			for _, line := range strings.Split(string(logs), "\n") {
				if strings.Contains(line, "LibraryMonitor:") || strings.Contains(line, "Scan Media Library") {
					t.Logf("%s Jellyfin: %s", environment, line)
				}
			}
		}
		_ = exec.Command("docker", "rm", "-f", fixture.container).Run()
	})
	fixture.refreshAddress(t)
	ctx := context.Background()
	watchersNeedReload := false
	pollJellyfin(t, time.Now().Add(2*time.Minute), "create the disposable libraries", func() (bool, error) {
		return true, fixture.client.ReconcileWatchedLibraries(ctx, fixture.credentials, func() error {
			watchersNeedReload = true
			return nil
		})
	})
	if !watchersNeedReload {
		t.Fatal("new libraries did not request a watcher reload")
	}
	key, err := fixture.client.ReconcileRefreshAPIKey(ctx, fixture.credentials, fixture.container, "")
	if err != nil {
		t.Fatal(err)
	}
	fixture.key = key
	var users []struct{ ID string }
	if err := fixture.request(http.MethodGet, "/Users", &users); err != nil || len(users) != 1 || users[0].ID == "" {
		t.Fatalf("observe disposable Jellyfin administrator identity: %v", err)
	}
	fixture.userID = users[0].ID
	pollJellyfin(t, time.Now().Add(time.Minute), "finish its initial library setup", func() (bool, error) {
		return fixture.scan(t).State == "Idle", nil
	})
	fixture.disableRemoteMetadata(t)
	refreshDocker(t, "restart", fixture.container)
	fixture.refreshAddress(t)
	pollJellyfin(t, time.Now().Add(time.Minute), "reload its watchers", func() (bool, error) {
		return true, fixture.client.Ready(ctx)
	})
	return fixture
}

func (fixture *refreshJellyfin) refreshAddress(t *testing.T) {
	t.Helper()
	fixture.address = publishedAddress(t, fixture.container, "8096/tcp")
	fixture.client = jellyfin.New(fixture.address, fixture.http)
}

func startRefreshArr(t *testing.T, image, project, service, port string) (string, string) {
	t.Helper()
	container := project + "-" + service
	refreshDocker(t, "run", "--detach", "--pull", "never", "--name", container, "--network", project, "--publish", "127.0.0.1::"+port, image)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-fv", container).Run() })
	address := publishedAddress(t, container, port+"/tcp")
	var settings struct{ ApiKey string }
	pollService(t, time.Now().Add(time.Minute), "wait for disposable "+service, func() error {
		contents, err := exec.Command("docker", "exec", container, "cat", "/config/config.xml").Output()
		if err != nil {
			return err
		}
		if err := xml.Unmarshal(contents, &settings); err != nil {
			return err
		}
		if settings.ApiKey == "" {
			return fmt.Errorf("missing generated API key")
		}
		if service == "radarr" {
			return radarr.New(address, settings.ApiKey, &http.Client{Timeout: time.Second}).Ready(context.Background())
		}
		return sonarr.New(address, settings.ApiKey, &http.Client{Timeout: time.Second}).Ready(context.Background())
	})
	return address, settings.ApiKey
}

func refreshDocker(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, output)
	}
}

type refreshScan struct {
	ID, Key, State string
	Triggers       json.RawMessage
	Result         struct {
		End    time.Time `json:"EndTimeUtc"`
		Status string
	} `json:"LastExecutionResult"`
}

func (fixture *refreshJellyfin) scan(t *testing.T) refreshScan {
	t.Helper()
	var tasks []refreshScan
	if err := fixture.request(http.MethodGet, "/ScheduledTasks", &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Key == "RefreshLibrary" {
			return task
		}
	}
	t.Fatal("missing Jellyfin recovery scan task")
	return refreshScan{}
}

func (fixture *refreshJellyfin) request(method, path string, output any) error {
	return fixture.requestJSON(method, path, nil, output)
}

func (fixture *refreshJellyfin) requestJSON(method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		contents, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(contents)
	}
	request, err := http.NewRequest(method, fixture.address+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", `MediaBrowser Client="media-stack", Device="media-stack", DeviceId="media-stack-cli", Version="1", Token="`+url.PathEscape(fixture.key)+`"`)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := fixture.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return fmt.Errorf("%s %s returned HTTP %d", method, path, response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}

func (fixture *refreshJellyfin) disableRemoteMetadata(t *testing.T) {
	t.Helper()
	var libraries []struct {
		ItemID         string         `json:"ItemId"`
		LibraryOptions map[string]any `json:"LibraryOptions"`
	}
	if err := fixture.request(http.MethodGet, "/Library/VirtualFolders", &libraries); err != nil || len(libraries) != 2 {
		t.Fatalf("observe disposable library options: %v", err)
	}
	for _, library := range libraries {
		var types []map[string]any
		for _, kind := range []string{"Movie", "Series", "Season", "Episode"} {
			types = append(types, map[string]any{"Type": kind, "MetadataFetchers": []string{}, "ImageFetchers": []string{}})
		}
		// The generated files have no provider IDs. Keep public metadata and
		// image services off the timing path of these local indexing checks.
		library.LibraryOptions["TypeOptions"] = types
		body := map[string]any{"Id": library.ItemID, "LibraryOptions": library.LibraryOptions}
		if err := fixture.requestJSON(http.MethodPost, "/Library/VirtualFolders/LibraryOptions", body, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func (fixture *refreshJellyfin) configureScan(t *testing.T, minutes int) {
	t.Helper()
	if err := fixture.client.ReconcileRefreshPolicy(context.Background(), fixture.credentials, minutes, func() error {
		return fmt.Errorf("new libraries should already have real-time monitoring enabled")
	}); err != nil {
		t.Fatal(err)
	}
	var observed []struct {
		Type           string
		TimeOfDayTicks int64
	}
	if err := json.Unmarshal(fixture.scan(t).Triggers, &observed); err != nil || len(observed) != 1 || observed[0].Type != "DailyTrigger" || observed[0].TimeOfDayTicks != int64(minutes)*600000000 {
		t.Fatalf("daily scan readback = %#v (%v)", observed, err)
	}
}

func (fixture *refreshJellyfin) file(name string) string {
	return filepath.Join(fixture.media, filepath.FromSlash(name))
}

func (fixture *refreshJellyfin) publish(t *testing.T, name string, contents []byte) {
	t.Helper()
	path := fixture.file(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(filepath.Dir(fixture.media), "incoming.mp4")
	if err := os.WriteFile(temporary, contents, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func (fixture *refreshJellyfin) waitPaths(t *testing.T, operation string, present, absent []string, baseline *refreshScan) {
	t.Helper()
	pollJellyfin(t, time.Now().Add(2*time.Minute), operation, func() (bool, error) {
		if baseline != nil {
			current := fixture.scan(t)
			if current.State != "Idle" || current.Result != baseline.Result {
				t.Fatalf("a full-library scan ran during %s", operation)
			}
		}
		paths, err := fixture.paths()
		if err != nil {
			return false, err
		}
		for _, file := range present {
			if !slices.Contains(paths, file) {
				return false, fmt.Errorf("missing path %q; indexed paths: %v", file, paths)
			}
		}
		for _, file := range absent {
			if slices.Contains(paths, file) {
				return false, fmt.Errorf("stale path %q remains indexed", file)
			}
		}
		return true, nil
	})
}

func (fixture *refreshJellyfin) paths() ([]string, error) {
	var result struct{ Items []struct{ Path string } }
	if err := fixture.request(http.MethodGet, "/Users/"+fixture.userID+"/Items?Recursive=true&IncludeItemTypes=Movie,Episode&Fields=Path", &result); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		paths = append(paths, strings.TrimPrefix(item.Path, "/data/media/"))
	}
	return paths, nil
}
