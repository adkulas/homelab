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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/seerr"
)

func TestDisposableSeerrAuthenticatesHouseholdAndEmergencyLocalUsers(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_SEERR") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_SEERR=1 to run the pinned disposable Seerr authentication acceptance test")
	}
	versions, err := config.LoadVersions(filepath.Join(repositoryRoot(t), "stacks", "media", "versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	for _, directory := range []string{
		filepath.Join(temporary, "jellyfin-config"),
		filepath.Join(temporary, "jellyfin-cache"),
		filepath.Join(temporary, "seerr-config"),
		filepath.Join(temporary, "media", "movies"),
		filepath.Join(temporary, "media", "series"),
	} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	suffix := fmt.Sprintf("%d", os.Getpid())
	networkName := "media-stack-seerr-acceptance-" + suffix
	if output, err := exec.Command("docker", "network", "create", networkName).CombinedOutput(); err != nil {
		t.Fatalf("create disposable application network: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", networkName).Run() })

	jellyfinName := "media-stack-jellyfin-seerr-acceptance-" + suffix
	startJellyfin := exec.Command("docker", "run", "--detach", "--rm", "--name", jellyfinName,
		"--network", networkName, "--network-alias", "jellyfin",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--publish", "127.0.0.1::8096",
		"--volume", filepath.Join(temporary, "jellyfin-config")+":/config",
		"--volume", filepath.Join(temporary, "jellyfin-cache")+":/cache",
		"--volume", filepath.Join(temporary, "media")+":/data/media:ro",
		versions.Images["jellyfin"])
	if output, err := startJellyfin.CombinedOutput(); err != nil {
		t.Fatalf("start disposable Jellyfin: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", jellyfinName).Run() })
	jellyfinAddress := publishedAddress(t, jellyfinName, "8096/tcp")

	administrator := jellyfin.Credentials{Username: "household-admin", Password: "fixture-jellyfin-password"}
	jellyfinClient := jellyfin.New(jellyfinAddress, &http.Client{Timeout: 10 * time.Second})
	deadline := time.Now().Add(2 * time.Minute)
	pollService(t, deadline, "reconcile disposable Jellyfin", func() error {
		return jellyfinClient.ReconcileLibraries(context.Background(), administrator)
	})
	household := seerr.Credentials{Username: "household-viewer", Password: "fixture-household-password"}
	createJellyfinUser(t, jellyfinAddress, administrator, household)

	seerrName := "media-stack-seerr-acceptance-" + suffix
	startSeerr := exec.Command("docker", "run", "--detach", "--rm", "--name", seerrName,
		"--network", networkName,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--publish", "127.0.0.1::5055",
		"--volume", filepath.Join(temporary, "seerr-config")+":/app/config",
		versions.Images["seerr"])
	if output, err := startSeerr.CombinedOutput(); err != nil {
		t.Fatalf("start disposable Seerr: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", seerrName).Run() })
	seerrAddress := publishedAddress(t, seerrName, "5055/tcp")
	seerrClient, err := seerr.New(seerrAddress, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	seerrAdministrator := seerr.Credentials{Username: administrator.Username, Password: administrator.Password}
	pollService(t, deadline, "reconcile disposable Seerr authentication", func() error {
		return seerrClient.ReconcileAuthentication(context.Background(), seerrAdministrator, jellyfinAddress)
	})
	if err := seerrClient.VerifyJellyfinAuthentication(context.Background(), household); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("docker", "stop", jellyfinName).CombinedOutput(); err != nil {
		t.Fatalf("stop Jellyfin before emergency local sign-in: %v\n%s", err, output)
	}
	if err := seerrClient.VerifyLocalAdministrator(context.Background(), seerrAdministrator); err != nil {
		t.Fatal(err)
	}
}

func publishedAddress(t *testing.T, containerName, containerPort string) string {
	t.Helper()
	output, err := exec.Command("docker", "port", containerName, containerPort).Output()
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + strings.TrimSpace(string(output))
	if parsed, parseErr := url.Parse(address); parseErr != nil || parsed.Port() == "" {
		t.Fatalf("published address = %q: %v", address, parseErr)
	}
	return address
}

func createJellyfinUser(t *testing.T, baseURL string, administrator jellyfin.Credentials, household seerr.Credentials) {
	t.Helper()
	var authenticated struct {
		AccessToken string `json:"AccessToken"`
	}
	jellyfinRequest(t, baseURL, http.MethodPost, "/Users/AuthenticateByName", map[string]string{
		"Username": administrator.Username,
		"Pw":       administrator.Password,
	}, "", &authenticated)
	if authenticated.AccessToken == "" {
		t.Fatal("Jellyfin administrator authentication omitted its access token")
	}
	jellyfinRequest(t, baseURL, http.MethodPost, "/Users/New", map[string]string{
		"Name":     household.Username,
		"Password": household.Password,
	}, authenticated.AccessToken, nil)
}

func jellyfinRequest(t *testing.T, baseURL, method, path string, input any, token string, output any) {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", `MediaBrowser Client="media-stack", Device="media-stack", DeviceId="media-stack-cli", Version="1"`)
	if token != "" {
		request.Header.Set("X-Emby-Token", token)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("Jellyfin %s %s returned HTTP %d: %s", method, path, response.StatusCode, body)
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
}

func pollService(t *testing.T, deadline time.Time, behavior string, attempt func() error) {
	t.Helper()
	var lastErr error
	for {
		if err := attempt(); err == nil {
			return
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not %s: %v", behavior, lastErr)
		}
		time.Sleep(time.Second)
	}
}
