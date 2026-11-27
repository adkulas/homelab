package engine

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
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
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/hardware"
	"github.com/adkulas/homelab/internal/topology"
)

func TestCalibreOwnedEPUBDownloadsBothFormats(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_CALIBRE") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_CALIBRE=1 for the pinned Docker rehearsal")
	}
	root, err := RepositoryRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := config.LoadVersions(filepath.Join(root, "services/media/versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	defaults := config.Defaults{RuntimeUID: os.Getuid(), RuntimeGID: os.Getgid(), Timezone: "UTC", LANBindAddress: "127.0.0.1"}
	env := config.Environment{ProjectName: fmt.Sprintf("calibre-test-%d", time.Now().UnixNano()), DataRoot: t.TempDir()}
	// Use a real plan with valid ports for every service. Only Calibre-Web starts.
	fixture, err := config.Load(filepath.Join(root, "tests/fixtures/media-stack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	env.Ports = fixture.Spec.Environments["staging"].Ports
	env.Ports.CalibreWeb = port
	compose, err := topology.Render(defaults, env, fixture.Spec.Acquisition.VPN, versions.Images, t.TempDir(), hardware.Transcoding{}, false)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{compose: compose}
	t.Cleanup(func() {
		if out, err := runDockerCompose(context.Background(), plan, "down", "-v", "--remove-orphans"); err != nil {
			t.Errorf("cleanup: %v %s", err, out)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	const password = "disposable ebook rehearsal"
	if err := prepareCalibreWeb(ctx, plan, env, defaults, password); err != nil {
		t.Fatal(err)
	}
	if out, err := runDockerCompose(ctx, plan, "exec", "-T", "calibre-web", "sqlite3", "/config/cwa.db", "UPDATE cwa_settings SET auto_convert=0"); err != nil {
		t.Fatalf("introduce conversion drift: %v %s", err, out)
	}
	if err := observeCalibrePolicy(ctx, plan); !errors.Is(err, errPlannedMutation) {
		t.Fatalf("expected conversion drift: %v", err)
	}
	if err := prepareCalibreWeb(ctx, plan, env, defaults, password); err != nil {
		t.Fatal(err)
	}
	if err := observeCalibrePolicy(ctx, plan); err != nil {
		t.Fatalf("policy after repeated apply: %v", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	download := func(path, pass string) (int, []byte) {
		req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("admin", pass)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, body
	}
	if status, _ := download("/opds", "admin123"); status != 401 {
		t.Fatalf("factory credential returned %d", status)
	}
	if status, _ := download("/opds", password); status != 200 {
		t.Fatalf("household login returned %d", status)
	}
	// Generate an original test publication with the pinned image's converter.
	out, err := runDockerCompose(ctx, plan, "exec", "-T", "calibre-web", "sh", "-ec", "echo 'A lawful test publication.' > /tmp/owned.txt; ebook-convert /tmp/owned.txt /tmp/owned.epub --title 'Owned rehearsal' >/dev/null; cat /tmp/owned.epub")
	if err != nil {
		t.Fatalf("generate owned EPUB: %v %s", err, out)
	}
	epub := filepath.Join(env.DataRoot, "books/ebooks/ready/owned.epub")
	if err := os.WriteFile(epub, out, 0640); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(root, "services/media/import-ebook"), epub)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("publish: %v %s", err, msg)
	}
	type link struct {
		Href string `xml:"href,attr"`
	}
	var feed struct {
		Entries []struct {
			Title string `xml:"title"`
			Links []link `xml:"link"`
		} `xml:"entry"`
	}
	var epubURL, kindleURL string
	for epubURL == "" || kindleURL == "" {
		status, catalog := download("/opds/search?query=Owned", password)
		feed.Entries = nil
		if status != 200 || xml.Unmarshal(catalog, &feed) != nil {
			t.Fatalf("invalid OPDS search response: %d", status)
		}
		if len(feed.Entries) == 1 && feed.Entries[0].Title == "Owned rehearsal" {
			for _, l := range feed.Entries[0].Links {
				if strings.HasSuffix(l.Href, "/epub/") {
					epubURL = l.Href
				}
				if strings.HasSuffix(l.Href, "/azw3/") {
					kindleURL = l.Href
				}
			}
		}
		if epubURL != "" && kindleURL != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for both formats on one record")
		case <-time.After(time.Second):
		}
	}
	// Exercise the browser's session-cookie login and local download URL as well as OPDS.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := http.Client{Jar: jar, Timeout: 10 * time.Second}
	form, err := browser.Get(base + "/login")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(form.Body)
	form.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	token := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(page)
	if len(token) != 2 {
		t.Fatal("login form has no CSRF token")
	}
	login, err := browser.PostForm(base+"/login", url.Values{"username": {"admin"}, "password": {password}, "csrf_token": {string(token[1])}})
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()
	resource := strings.TrimSuffix(strings.TrimPrefix(kindleURL, "/opds"), "/")
	res, err := browser.Get(base + resource)
	if err != nil {
		t.Fatal(err)
	}
	kindle, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	status := res.StatusCode
	if status != 200 || !bytes.Contains(kindle, []byte("BOOKMOBI")) {
		t.Fatalf("invalid Kindle download: %d", status)
	}
	status, original := download(epubURL, password)
	if status != 200 || !bytes.HasPrefix(original, []byte("PK")) {
		t.Fatalf("invalid EPUB download: %d", status)
	}
	retained, err := os.ReadFile(epub)
	if err != nil || !bytes.Equal(retained, out) {
		t.Fatal("ready source changed")
	}
}
