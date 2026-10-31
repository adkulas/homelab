package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/companions"
	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/qbittorrent"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

func TestDisposablePinnedSeriesCompanions(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_COMPANIONS") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_COMPANIONS=1 for isolated real-image companion acceptance (allow several minutes)")
	}
	versions, err := config.LoadVersions(filepath.Join(repositoryRoot(t), "services/media/versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	suffix := fmt.Sprintf("issue88-%d-%d", os.Getpid(), time.Now().UnixNano())
	network := suffix + "-staging"
	docker := func(arguments ...string) string {
		t.Helper()
		output, err := exec.Command("docker", arguments...).CombinedOutput()
		if err != nil {
			t.Fatalf("disposable docker %s: %v\n%s", arguments[0], err, output)
		}
		return strings.TrimSpace(string(output))
	}
	docker("network", "create", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })
	start := func(name, image, mount string, port int, extra ...string) string {
		t.Helper()
		arguments := []string{"run", "--detach", "--rm", "--name", name, "--network", network, "--publish", fmt.Sprintf("127.0.0.1::%d", port)}
		if mount != "" {
			volume := name + "-state"
			docker("volume", "create", volume)
			t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", volume).Run() })
			arguments = append(arguments, "--volume", volume+":"+mount)
		}
		arguments = append(arguments, extra...)
		arguments = append(arguments, image)
		docker(arguments...)
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
		return publishedAddress(t, name, strconv.Itoa(port)+"/tcp")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	qbitPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	fixtureName := suffix + "-apis"
	fixtureScript := filepath.Join(repositoryRoot(t), "tests/acceptance/fixtures/companion_api.py")
	// Reuse the pinned Python runtime; the fixture replaces its entrypoint and
	// runs only in the disposable application network, with no external sources.
	docker("run", "--detach", "--rm", "--name", fixtureName, "--network", network,
		"--network-alias", "sonarr", "--network-alias", "qbittorrent", "--no-healthcheck",
		"--publish", "127.0.0.1::8989", "--publish", fmt.Sprintf("127.0.0.1:%d:%d", qbitPort, qbitPort),
		"--env", fmt.Sprintf("QBIT_PORT=%d", qbitPort), "--user", "1000:1000",
		"--volume", fixtureScript+":/fixture.py:ro", "--entrypoint", "python", versions.Images["houndarr"], "/fixture.py")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", fixtureName).Run() })
	fixtureAddress := publishedAddress(t, fixtureName, "8989/tcp")
	houndName, cleanupName := suffix+"-houndarr", suffix+"-cleanuparr"
	houndAddress := start(houndName, versions.Images["houndarr"], "/data", 8877, "--env", "TZ=America/Toronto")
	cleanupAddress := start(cleanupName, versions.Images["cleanuparr"], "/config", 11011)
	client := &http.Client{Timeout: 10 * time.Second}
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	waitHTTP := func(address string) {
		t.Helper()
		pollService(t, time.Now().Add(2*time.Minute), "disposable API startup", func() error {
			response, err := client.Get(address)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			if response.StatusCode >= 500 {
				return fmt.Errorf("HTTP %d", response.StatusCode)
			}
			return nil
		})
	}
	waitHTTP(fixtureAddress + "/fixture")
	waitHTTP(houndAddress + "/setup")
	waitHTTP(cleanupAddress + "/api/auth/status")
	liveRequest := func(method, address string, body any, token string) map[string]any {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(encoded)
		}
		request, err := http.NewRequest(method, address, reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		encoded, _ := io.ReadAll(response.Body)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("disposable %s %s: HTTP %d\n%s", method, address, response.StatusCode, encoded)
		}
		result := map[string]any{}
		if len(encoded) > 0 {
			_ = json.Unmarshal(encoded, &result)
		}
		return result
	}
	liveRequest("POST", cleanupAddress+"/api/auth/setup/account", map[string]string{"username": "companion-admin", "password": "fixture-admin-password"}, "")
	liveRequest("POST", cleanupAddress+"/api/auth/setup/complete", map[string]any{}, "")
	login := liveRequest("POST", cleanupAddress+"/api/auth/login", map[string]string{"username": "companion-admin", "password": "fixture-admin-password"}, "")
	token := login["tokens"].(map[string]any)["accessToken"].(string)
	cleanupKey := liveRequest("GET", cleanupAddress+"/api/account/api-key", nil, token)["apiKey"].(string)
	houndForm := func(path string, form url.Values) string {
		t.Helper()
		request, err := http.NewRequest("POST", houndAddress+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if path != "/setup" && path != "/login" {
			response, err := client.Get(houndAddress + "/settings")
			if err != nil {
				t.Fatal(err)
			}
			node, err := html.Parse(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			var visit func(*html.Node)
			visit = func(n *html.Node) {
				name, value := "", ""
				for _, attr := range n.Attr {
					if attr.Key == "name" {
						name = attr.Val
					}
					if attr.Key == "value" {
						value = attr.Val
					}
				}
				if n.Data == "input" && name == "csrf_token" {
					request.Header.Set("X-CSRF-Token", value)
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					visit(child)
				}
			}
			visit(node)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		encoded, _ := io.ReadAll(response.Body)
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			t.Fatalf("Houndarr disposable %s: HTTP %d\n%s", path, response.StatusCode, encoded)
		}
		return string(encoded)
	}
	houndForm("/setup", url.Values{"username": {"companion-admin"}, "password": {"fixture-houndarr-password"}, "password_confirm": {"fixture-houndarr-password"}})
	houndForm("/login", url.Values{"username": {"companion-admin"}, "password": {"fixture-houndarr-password"}})
	policy := config.SeriesAutomation{Enabled: true, Search: config.EpisodeSearchPolicy{BatchSize: 12, IntervalMinutes: 30, HourlyCap: 12, CooldownDays: 1, ReleaseGraceHours: 6, AllowedWindow: "06:00-00:00", QueueLimit: 2}, Recovery: config.QueueRecoveryPolicy{IntervalMinutes: 5, Strikes: 12, MinimumSpeedKiB: 100, MaximumRemainingHours: 24}}
	form := url.Values{"name": {"Sonarr"}, "type": {"sonarr"}, "url": {"http://sonarr:8989"}, "api_key": {"fixture-sonarr-api-key"}, "connection_verified": {"true"}, "sonarr_search_mode": {"episode"}}
	for key, value := range companions.HoundarrFields(policy) {
		form.Set(key, value)
	}
	houndForm("/settings/instances/test-connection", form)
	body := houndForm("/settings/instances", form)
	match := regexp.MustCompile(`/settings/instances/([0-9]+)/edit`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("Houndarr did not create its disposable instance")
	}
	instance := match[1]
	cleanupClient := companions.NewCleanuparr(cleanupAddress, cleanupKey, client)
	qbit := qbittorrent.DeclaredConfiguration{Credentials: qbittorrent.Credentials{Username: "household", Password: "fixture-qbittorrent-password"}, Port: qbitPort}
	if err := cleanupClient.Reconcile(context.Background(), policy, "fixture-sonarr-api-key", qbit, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			response, err := client.Get(fixtureAddress + "/fixture")
			if err == nil {
				defer response.Body.Close()
				var state map[string]any
				if json.NewDecoder(response.Body).Decode(&state) == nil {
					requests, _ := state["requests"].([]any)
					if len(requests) > 30 {
						requests = requests[len(requests)-30:]
					}
					t.Logf("Disposable API recent request paths: %v", requests)
				}
			}
		}
	})
	fixtureState := func() map[string]any { return liveRequest("GET", fixtureAddress+"/fixture", nil, "") }
	houndForm("/api/instances/"+instance+"/run-now", url.Values{})
	pollService(t, time.Now().Add(90*time.Second), "real Houndarr episode dispatch", func() error {
		commands := fixtureState()["commands"].([]any)
		if len(commands) != 12 {
			return fmt.Errorf("observed %d commands, want 12", len(commands))
		}
		return nil
	})
	for _, raw := range fixtureState()["commands"].([]any) {
		command := raw.(map[string]any)
		if command["name"] != "EpisodeSearch" {
			t.Fatal("proactive search used non-episode context")
		}
		for _, rawID := range command["body"].(map[string]any)["episodeIds"].([]any) {
			id := int(rawID.(float64))
			if id < 101 || id > 114 || id == 103 {
				t.Fatalf("searched ineligible fixture episode %d", id)
			}
		}
	}
	houndForm("/api/instances/"+instance+"/run-now", url.Values{})
	time.Sleep(3 * time.Second)
	if len(fixtureState()["commands"].([]any)) != 12 {
		t.Fatal("real Houndarr exceeded the declared hourly cap")
	}

	// A higher cap isolates backpressure from cap exhaustion for this check.
	liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": true}, "")
	form.Set("api_key", "__UNCHANGED__")
	form.Set("hourly_cap", "48")
	houndForm("/settings/instances/"+instance, form)
	houndForm("/api/instances/"+instance+"/run-now", url.Values{})
	time.Sleep(3 * time.Second)
	if len(fixtureState()["commands"].([]any)) != 12 {
		t.Fatal("real Houndarr ignored queue backpressure")
	}
	form.Set("hourly_cap", "12")
	houndForm("/settings/instances/"+instance, form)
	liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": false, "wanted": false, "queuedEpisode": 0}, "")
	t.Log("Real Houndarr proved episode context, eligibility, hourly cap and queue backpressure")
	// Each new hash has independent strike history. Trigger the supported job
	// endpoint instead of waiting twelve five-minute scheduler intervals.
	for index, reason := range []string{"stalled", "metadata", "slow-speed", "slow-time", "failed-import"} {
		t.Run(reason, func(t *testing.T) {
			hash := strings.Repeat(fmt.Sprintf("%x", index+1), 40)
			liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": true, "blocked": false, "torrent": true, "mode": reason, "hash": hash, "wanted": false, "queuedEpisode": 0}, "")
			before := len(fixtureState()["commands"].([]any))

			observed := 0
			for observed < 12 {
				liveRequest("POST", cleanupAddress+"/api/jobs/QueueCleaner/trigger", map[string]any{}, token)
				previous := observed
				pollService(t, time.Now().Add(20*time.Second), "real Cleanuparr qualifying observation", func() error {
					events, err := cleanupClient.ObserveQueue(context.Background(), time.Now().Add(-time.Hour))
					if err != nil {
						return err
					}
					for _, event := range events {
						if event.ItemHash == hash && event.StrikeCount > observed {
							observed = event.StrikeCount
						}
					}
					if observed > previous {
						return nil
					}
					return fmt.Errorf("observation after %d not recorded", previous)
				})
				if observed < 12 {
					state := fixtureState()
					if state["blocked"] == true || state["queued"] != true || state["torrent"] != true {
						t.Fatal("real Cleanuparr cleaned below the declared threshold")
					}
				}
			}
			pollService(t, time.Now().Add(30*time.Second), "real Cleanuparr removal and blocklisting", func() error {
				state := fixtureState()
				if state["queued"] == true || state["torrent"] == true || state["blocked"] != true {
					return fmt.Errorf("recovery not complete")
				}
				return nil
			})
			pollService(t, time.Now().Add(3*time.Minute), "real Cleanuparr replacement EpisodeSearch", func() error {
				commands := fixtureState()["commands"].([]any)
				if len(commands) <= before {
					return fmt.Errorf("replacement not dispatched")
				}
				command := commands[len(commands)-1].(map[string]any)
				if command["name"] != "EpisodeSearch" {
					return fmt.Errorf("replacement used %v", command["name"])
				}
				return nil
			})
		})
	}

	waitThreshold := func(hash string) {
		t.Helper()
		observed := 0
		for observed < 12 {
			liveRequest("POST", cleanupAddress+"/api/jobs/QueueCleaner/trigger", map[string]any{}, token)
			previous := observed
			pollService(t, time.Now().Add(20*time.Second), "actual repeated qualifying observations", func() error {
				events, err := cleanupClient.ObserveQueue(context.Background(), time.Now().Add(-time.Hour))
				if err != nil {
					return err
				}
				for _, event := range events {
					if event.ItemHash == hash && event.StrikeCount > observed {
						observed = event.StrikeCount
					}
				}
				if observed > previous {
					return nil
				}
				return fmt.Errorf("next qualifying observation not recorded")
			})
		}
	}
	t.Run("connectivity outage", func(t *testing.T) {
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": true, "torrent": true, "blocked": false, "mode": "stalled", "hash": strings.Repeat("6", 40), "clientUnavailable": true, "wanted": false, "queuedEpisode": 0}, "")
		before := len(fixtureState()["commands"].([]any))
		for observation := 0; observation < 3; observation++ {
			liveRequest("POST", cleanupAddress+"/api/jobs/QueueCleaner/trigger", map[string]any{}, token)
			time.Sleep(time.Second)
		}
		state := fixtureState()
		if state["queued"] != true || state["torrent"] != true || state["blocked"] != false || len(state["commands"].([]any)) != before {
			t.Fatal("a shared client outage caused destructive recovery")
		}
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"clientUnavailable": false}, "")
	})
	t.Run("blocklist failure", func(t *testing.T) {
		hash := strings.Repeat("7", 40)
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": true, "torrent": true, "blocked": false, "mode": "stalled", "hash": hash, "blocklistFailure": true, "wanted": false, "queuedEpisode": 0}, "")
		before := len(fixtureState()["commands"].([]any))
		waitThreshold(hash)
		time.Sleep(2 * time.Second)
		state := fixtureState()
		if state["queued"] != true || state["torrent"] != true || state["blocked"] != false || len(state["commands"].([]any)) != before {
			t.Fatal("a rejected blocklist operation allowed removal or replacement")
		}
		summary := liveRequest("GET", cleanupAddress+"/api/seeker/search-stats/summary", nil, token)
		if summary["pendingReplacementSearches"] != float64(0) {
			t.Fatal("a failed blocklist operation queued a replacement")
		}
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"blocklistFailure": false, "queued": false}, "")
	})
	t.Run("cooldown and quiet hours", func(t *testing.T) {
		policy.Search.HourlyCap = 48
		form.Set("hourly_cap", "48")
		location, err := time.LoadLocation("America/Toronto")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().In(location)
		policy.Search.AllowedWindow = now.Add(-time.Hour).Format("15:04") + "-" + now.Add(time.Hour).Format("15:04")
		form.Set("allowed_time_window", policy.Search.AllowedWindow)
		houndForm("/settings/instances/"+instance, form)
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": false, "wanted": true, "queuedEpisode": 0}, "")
		before := len(fixtureState()["commands"].([]any))
		docker("restart", houndName)
		houndAddress = publishedAddress(t, houndName, "8877/tcp")
		waitHTTP(houndAddress + "/login")
		pollService(t, time.Now().Add(30*time.Second), "scheduled cooldown-aware searches", func() error {
			if len(fixtureState()["commands"].([]any)) != before+2 {
				return fmt.Errorf("expected only the two never-searched episodes")
			}
			return nil
		})
		time.Sleep(4 * time.Second)
		if len(fixtureState()["commands"].([]any)) != before+2 {
			t.Fatal("scheduled searches ignored persisted cooldowns")
		}
		excluded := now.Add(3*time.Hour).Format("15:04") + "-" + now.Add(4*time.Hour).Format("15:04")
		form.Set("allowed_time_window", excluded)
		houndForm("/settings/instances/"+instance, form)
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"idOffset": 100, "episodeCount": 2}, "")
		docker("restart", houndName)
		houndAddress = publishedAddress(t, houndName, "8877/tcp")
		waitHTTP(houndAddress + "/login")
		pollService(t, time.Now().Add(20*time.Second), "real scheduled quiet-hour gate", func() error {
			response, err := client.Get(houndAddress + "/api/logs?limit=200")
			if err != nil {
				return err
			}
			defer response.Body.Close()
			var rows []struct {
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(response.Body).Decode(&rows); err != nil {
				return err
			}
			for _, row := range rows {
				if strings.Contains(row.Reason, "outside allowed time window") {
					return nil
				}
			}
			return fmt.Errorf("quiet-hour observation not recorded")
		})
		if len(fixtureState()["commands"].([]any)) != before+2 {
			t.Fatal("quiet hours dispatched new episode searches")
		}

		form.Set("allowed_time_window", policy.Search.AllowedWindow)
		houndForm("/settings/instances/"+instance, form)
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"idOffset": 200, "recentRelease": true}, "")
		docker("restart", houndName)
		houndAddress = publishedAddress(t, houndName, "8877/tcp")
		waitHTTP(houndAddress + "/login")
		pollService(t, time.Now().Add(20*time.Second), "scheduled release grace", func() error {
			response, err := client.Get(houndAddress + "/api/logs?limit=200")
			if err != nil {
				return err
			}
			defer response.Body.Close()
			var rows []struct {
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(response.Body).Decode(&rows); err != nil {
				return err
			}
			for _, row := range rows {
				if strings.Contains(row.Reason, "post-release grace") {
					return nil
				}
			}
			return fmt.Errorf("release-grace observation not recorded")
		})
		if len(fixtureState()["commands"].([]any)) != before+2 {
			t.Fatal("scheduled search ignored release grace or future air date")
		}
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"wanted": false, "idOffset": 100, "recentRelease": false}, "")
	})
	t.Run("season pack and eventual episode fallback", func(t *testing.T) {
		hash := strings.Repeat("8", 40)
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"queued": true, "torrent": true, "blocked": false, "mode": "season", "hash": hash, "wanted": false, "queuedEpisode": 0, "idOffset": 100, "episodeCount": 2}, "")
		before := len(fixtureState()["commands"].([]any))
		waitThreshold(hash)
		pollService(t, time.Now().Add(3*time.Minute), "actual SeasonSearch replacement", func() error {
			commands := fixtureState()["commands"].([]any)
			if len(commands) <= before {
				return fmt.Errorf("pack replacement not dispatched")
			}
			last := commands[len(commands)-1].(map[string]any)
			if last["name"] != "SeasonSearch" {
				return fmt.Errorf("pack replacement was not a SeasonSearch")
			}
			return nil
		})
		liveRequest("POST", fixtureAddress+"/fixture", map[string]any{"wanted": true}, "")
		before = len(fixtureState()["commands"].([]any))
		docker("restart", houndName)
		houndAddress = publishedAddress(t, houndName, "8877/tcp")
		waitHTTP(houndAddress + "/login")
		pollService(t, time.Now().Add(30*time.Second), "scheduled episode fallback after pack replacement", func() error {
			commands := fixtureState()["commands"].([]any)
			if len(commands) != before+2 {
				return fmt.Errorf("episode fallback not complete")
			}
			for _, raw := range commands[before:] {
				if raw.(map[string]any)["name"] != "EpisodeSearch" {
					return fmt.Errorf("fallback was not episode-context")
				}
			}
			return nil
		})
	})
	// Verify the actual pinned services through the CLI public seam as well.
	declared, err := config.Load(filepath.Join(repositoryRoot(t), "services/media/media-stack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	environment := declared.Spec.Environments["staging"]
	environment.SeriesAutomation = &policy
	environment.Ports.Houndarr = fixturePort(t, houndAddress)
	environment.Ports.Cleanuparr = fixturePort(t, cleanupAddress)
	environment.Ports.Seerr = fixturePort(t, fixtureAddress)
	environment.Ports.Sonarr = fixturePort(t, fixtureAddress)
	environment.Ports.QBittorrent = qbitPort
	declared.Spec.Environments["staging"] = environment
	configuration, err := yaml.Marshal(declared)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temporary, "media-stack.yaml")
	writeFile(t, configPath, configuration, 0600)
	bin := filepath.Join(temporary, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "docker"), "#!/bin/sh\ncat >/dev/null\nprintf '<Config><ApiKey>fixture-sonarr-api-key</ApiKey></Config>'\n")
	secrets := fmt.Sprintf("nordvpn:\n  openvpn:\n    serviceUsername: fixture-user\n    servicePassword: fixture-password\nprofilarr:\n  apiKey: fixture-profilarr-api-key-32-characters\njellyfin:\n  username: household\n  password: fixture-jellyfin-password\nqbittorrent:\n  username: household\n  password: fixture-qbittorrent-password\nhoundarr:\n  username: companion-admin\n  password: fixture-houndarr-password\ncleanuparr:\n  apiKey: %s\n", cleanupKey)
	writeFile(t, filepath.Join(temporary, "fixture-secrets.yaml"), []byte(secrets), 0600)
	writeExecutable(t, filepath.Join(bin, "sops"), "#!/bin/sh\ncat '"+filepath.Join(temporary, "fixture-secrets.yaml")+"'\n")
	command := exec.Command("go", "run", "./cmd/media-stack", "verify", "--environment", "staging", "--config", configPath, "--suite", "companions", "--output", "json")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real-image CLI verification failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "VERIFY_COMPANION_POLICY_CONVERGED") {
		t.Fatal("CLI did not verify pinned companion policy")
	}
}
