package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/audiobookshelf"
	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/hardware"
	"github.com/adkulas/homelab/internal/topology"
)

func TestDisposableAudiobookshelfPlayback(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_AUDIOBOOKSHELF") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_AUDIOBOOKSHELF=1 to run the pinned server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	versions, err := config.LoadVersions("../../services/media/versions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	env := config.Environment{ProjectName: fmt.Sprintf("abs-test-%d", time.Now().UnixNano()), DataRoot: root, Ports: config.Ports{QBittorrent: 8080}}
	libraryDir := filepath.Join(root, "books", "audiobooks")
	if err := os.MkdirAll(libraryDir, 0755); err != nil {
		t.Fatal(err)
	}
	compose, err := topology.Render(config.Defaults{Timezone: "UTC", RuntimeUID: 1000, RuntimeGID: 1000, LANBindAddress: "127.0.0.1"}, env, config.VPN{}, versions.Images, root, hardware.Transcoding{}, false)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{compose: compose}
	creds := audiobookshelf.Credentials{Username: "household", Password: "fixture-household-password"}
	setup, err := topology.AudiobookshelfSetup(compose)
	if err != nil {
		t.Fatal(err)
	}
	setupPlan := Plan{compose: setup}
	t.Cleanup(func() {
		if out, err := runDockerCompose(context.Background(), setupPlan, "down", "--volumes"); err != nil {
			t.Errorf("clean up isolated setup: %v: %s", err, out)
		}
	})
	_, initialized, err := startAudioServer(ctx, setupPlan, true)
	if err != nil || initialized {
		t.Fatalf("fresh setup initialized=%v: %v", initialized, err)
	}
	id, err := runDockerCompose(ctx, setupPlan, "ps", "-q", "audiobookshelf")
	if err != nil {
		t.Fatal(err)
	}
	networks, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{range $name, $network := .NetworkSettings.Networks}}{{$name}}{{end}}", strings.TrimSpace(string(id))).Output()
	if err != nil || strings.TrimSpace(string(networks)) != env.ProjectName+"_audiobookshelf-setup" {
		t.Fatalf("uninitialized server networks %q: %v", networks, err)
	}
	for i := 0; i < 2; i++ {
		if err := startAudiobookshelf(ctx, plan, env.ProjectName, creds); err != nil {
			t.Fatal(err)
		}
	}
	client, err := audioPublishedClient(ctx, plan, false)
	if err != nil {
		t.Fatal(err)
	}
	api := func(method, path, token string, body, result any) []byte {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if body == nil {
			payload = nil
		}
		req, err := http.NewRequestWithContext(ctx, method, client.URL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			t.Fatalf("%s %s returned %d", method, path, res.StatusCode)
		}
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			if err := json.Unmarshal(data, result); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return data
	}
	login := func(creds audiobookshelf.Credentials) string {
		var session struct{ User struct{ AccessToken string } }
		api("POST", "/login", "", creds, &session)
		if session.User.AccessToken == "" {
			t.Fatal("login returned no access token")
		}
		return session.User.AccessToken
	}
	admin := login(creds)
	var libraries struct{ Libraries []struct{ ID string } }
	api("GET", "/api/libraries", admin, nil, &libraries)
	if len(libraries.Libraries) != 1 {
		t.Fatalf("found %d libraries after repeated apply", len(libraries.Libraries))
	}
	lib := "/api/libraries/" + libraries.Libraries[0].ID

	audio, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--entrypoint", "ffmpeg", versions.Images["audiobookshelf"], "-v", "error", "-f", "lavfi", "-i", "sine=duration=12", "-f", "mp3", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(root, "incoming")
	if err := os.MkdirAll(incoming, 0755); err != nil {
		t.Fatal(err)
	}
	author := filepath.Join(libraryDir, "Fixture Author")
	if err := os.MkdirAll(author, 0755); err != nil {
		t.Fatal(err)
	}
	for _, track := range []string{"01.mp3", "02.mp3"} {
		if err := os.WriteFile(filepath.Join(incoming, track), audio, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(incoming, filepath.Join(author, "Fixture Book")); err != nil {
		t.Fatal(err)
	}
	api("POST", lib+"/scan", admin, nil, nil)
	var items struct{ Results []struct{ ID string } }
	for deadline := time.Now().Add(30 * time.Second); ; {
		api("GET", lib+"/items", admin, nil, &items)
		if len(items.Results) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("found %d books after scanning two tracks", len(items.Results))
		}
		time.Sleep(300 * time.Millisecond)
	}
	item := "/api/items/" + items.Results[0].ID
	listener := audiobookshelf.Credentials{Username: "listener", Password: "fixture-listener-password"}
	api("POST", "/api/users", admin, map[string]any{"username": listener.Username, "password": listener.Password, "isActive": true, "type": "user", "permissions": map[string]bool{"download": true}}, nil)
	member := login(listener)
	var session struct{ AudioTracks []struct{ ContentURL string } }
	api("POST", item+"/play", member, map[string]any{"forceDirectPlay": true, "supportedMimeTypes": []string{"audio/mpeg"}, "mediaPlayer": "html5"}, &session)
	if len(session.AudioTracks) != 2 {
		t.Fatalf("playback returned %d tracks", len(session.AudioTracks))
	}
	for _, track := range session.AudioTracks {
		if !bytes.Equal(api("GET", track.ContentURL, member, nil, nil), audio) {
			t.Fatal("streamed audio differs from imported track")
		}
	}
	var detail struct{ LibraryFiles []struct{ Ino string } }
	api("GET", item+"?expanded=1", member, nil, &detail)
	if len(detail.LibraryFiles) != 2 {
		t.Fatalf("found %d downloadable files", len(detail.LibraryFiles))
	}
	if !bytes.Equal(api("GET", item+"/file/"+detail.LibraryFiles[0].Ino+"/download", member, nil, nil), audio) {
		t.Fatal("downloaded track differs from imported audio")
	}

	progress := "/api/me/progress/" + items.Results[0].ID
	for token, position := range map[string]int{admin: 3, member: 7} {
		api("PATCH", progress, token, map[string]any{"duration": 24, "currentTime": position, "progress": float64(position) / 24}, nil)
	}
	if err := startAudiobookshelf(ctx, plan, env.ProjectName, creds); err != nil {
		t.Fatal(err)
	}
	for token, want := range map[string]int{admin: 3, member: 7} {
		var state struct{ CurrentTime float64 }
		api("GET", progress, token, nil, &state)
		if state.CurrentTime != float64(want) {
			t.Errorf("recovered listening position %v, want %d", state.CurrentTime, want)
		}
	}
}
