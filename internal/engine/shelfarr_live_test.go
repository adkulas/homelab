package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/hardware"
	"github.com/adkulas/homelab/internal/shelfarr"
	"github.com/adkulas/homelab/internal/topology"
)

func TestDisposableShelfarrRequestsAndSettings(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_SHELFARR") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_SHELFARR=1 for the pinned service and Open Library")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	versions, err := config.LoadVersions("../../services/media/versions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project := fmt.Sprintf("shelfarr-test-%d", time.Now().UnixNano())
	declared, err := config.Load("../../tests/fixtures/media-stack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env := config.Environment{ProjectName: project, DataRoot: root, Ports: declared.Spec.Environments["staging"].Ports}
	env.Ports.Shelfarr = 0
	defaults := config.Defaults{Timezone: "UTC", RuntimeUID: 1000, RuntimeGID: 1000, LANBindAddress: "127.0.0.1"}
	compose, err := topology.Render(defaults, env, config.VPN{}, versions.Images, root, hardware.Transcoding{}, false)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{compose: compose}
	setup, err := topology.ShelfarrSetup(compose)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if out, err := runDockerCompose(context.Background(), Plan{compose: setup}, "down", "--volumes"); err != nil {
			t.Errorf("clean up disposable Shelfarr: %v: %s", err, out)
		}
	})
	const password = "ShelfarrHousehold1"
	if err := startShelfarr(ctx, plan, project, password, ""); err != nil {
		t.Fatal(err)
	}
	base, err := publishedWebAddress(ctx, plan, "shelfarr", true)
	if err != nil {
		t.Fatal(err)
	}
	newSession := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	request := func(client *http.Client, method, path, csrf string, form url.Values, want int) (string, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Origin", base)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("Shelfarr %s %s returned HTTP %d, want %d", method, path, res.StatusCode, want)
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body), res.Header.Get("Location")
	}
	tokenPattern := regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
	csrf := func(page string) string {
		t.Helper()
		matches := tokenPattern.FindStringSubmatch(page)
		if matches == nil {
			t.Fatal("Shelfarr page has no CSRF token")
		}
		return matches[1]
	}
	login := func(username string) *http.Client {
		client := newSession()
		page, _ := request(client, "GET", "/session/new", "", nil, 200)
		request(client, "POST", "/session", csrf(page), url.Values{"username": {username}, "password": {password}}, 302)
		return client
	}

	// A regular local user can search and submit each format without a token.
	admin := login("household")
	userForm, _ := request(admin, "GET", "/admin/users/new", "", nil, 200)
	_, location := request(admin, "POST", "/admin/users", csrf(userForm), url.Values{
		"user[name]": {"Reader"}, "user[username]": {"reader"}, "user[role]": {"user"},
		"user[password]": {password}, "user[password_confirmation]": {password},
	}, 302)
	if !strings.HasSuffix(location, "/admin/users") {
		t.Fatal("Shelfarr did not create the reader account")
	}
	reader := login("reader")
	for _, query := range []string{"Pride and Prejudice", "Jane Austen"} {
		results, _ := request(reader, "GET", "/search/results?q="+url.QueryEscape(query), "", nil, 200)
		if !strings.Contains(results, "Pride and Prejudice") || !strings.Contains(results, "Open Library") {
			t.Fatalf("Open Library search did not find the expected work for %q", query)
		}
	}
	page, _ := request(reader, "GET", "/search", "", nil, 200)
	requests := map[string]string{}
	for _, format := range []string{"ebook", "audiobook"} {
		_, location := request(reader, "POST", "/requests", csrf(page), url.Values{
			"work_id": {"OL66554W"}, "title": {"Pride and Prejudice"}, "author": {"Jane Austen"},
			"book_type": {format}, "content_kind": {"book"},
		}, 302)
		if !regexp.MustCompile(`/requests/\d+$`).MatchString(location) {
			t.Fatalf("Shelfarr did not create the %s request", format)
		}
		requests[format] = location
		parsed, err := url.Parse(location)
		if err != nil {
			t.Fatal(err)
		}
		state := regexp.MustCompile(`<span role="status" aria-live="polite"[^>]*>\s*([^<]+)`)
		deadline := time.Now().Add(15 * time.Second)
		for {
			details, _ := request(reader, "GET", parsed.Path, "", nil, 200)
			if match := state.FindStringSubmatch(details); match != nil && strings.TrimSpace(match[1]) != "Pending" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Shelfarr did not start the %s request automatically", format)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	if requests["ebook"] == requests["audiobook"] {
		t.Fatal("ebook and audiobook requests must have distinct IDs")
	}

	for _, token := range []string{"fixture-token-one", "fixture-token-one", "fixture-token-two"} {
		if err := startShelfarr(ctx, plan, project, password, token); err != nil {
			t.Fatal(err)
		}
		visible, err := shelfarr.New(base, observationHTTPClient()).SettingsMatch(ctx, password, true)
		if err != nil || !visible {
			t.Fatalf("Shelfarr plan could not observe the visible settings: %v", err)
		}
		// The browser intentionally masks this field. Check that the pinned
		// app resolves the written token without printing either value.
		const check = `abort unless SettingsService.get(:hardcover_api_token) == ARGV.first`
		if _, err := runDockerCompose(ctx, plan, "exec", "-T", "shelfarr", "./bin/docker-entrypoint", "./bin/rails", "runner", check, token); err != nil {
			t.Fatal("Shelfarr did not retain the token written through authenticated settings")
		}
	}
}
