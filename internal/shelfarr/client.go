// Package shelfarr reconciles the pinned Shelfarr release through its local web UI.
package shelfarr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const settingsPath = "/admin/settings"

type Client struct {
	base string
	http http.Client
}

func New(base string, httpClient *http.Client) *Client {
	// Each reconciliation gets its own Rails session and CSRF tokens.
	jar, _ := cookiejar.New(nil)
	client := *httpClient
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base, http: client}
}

func (c *Client) NeedsSetup(ctx context.Context) (bool, error) {
	status, location, _, err := c.request(ctx, http.MethodGet, "/session/new?local=1", "", nil)
	if err != nil {
		return false, err
	}
	if status == http.StatusOK {
		return false, nil
	}
	if status == http.StatusFound && hasPath(location, "/sign_up") {
		return true, nil
	}
	return false, fmt.Errorf("Shelfarr returned unexpected login status HTTP %d", status)
}

func (c *Client) Initialize(ctx context.Context, password string) error {
	csrf, err := c.formToken(ctx, "/sign_up")
	if err != nil {
		return err
	}
	status, location, _, err := c.request(ctx, http.MethodPost, "/sign_up", csrf, url.Values{
		"user[name]": {"Household"}, "user[username]": {"household"},
		"user[password]": {password}, "user[password_confirmation]": {password},
	})
	if err != nil {
		return err
	}
	if status != http.StatusFound || !hasPath(location, "/admin/setup_wizard") {
		if status == http.StatusUnprocessableEntity && !AcceptsInitialPassword(password) {
			return fmt.Errorf("%s %s", PasswordRequirement, PasswordRemedy)
		}
		return fmt.Errorf("Shelfarr rejected initial account setup (HTTP %d)", status)
	}
	return nil
}

func (c *Client) Reconcile(ctx context.Context, password, hardcoverToken string) error {
	if err := c.signIn(ctx, password); err != nil {
		return err
	}
	csrf, err := c.formToken(ctx, settingsPath)
	if err != nil {
		return fmt.Errorf("Shelfarr household sign-in did not grant access to admin settings: %w", err)
	}
	settings := url.Values{
		"settings[open_library_enabled]":     {"true"},
		"settings[metadata_source]":          {"auto"},
		"settings[immediate_search_enabled]": {"true"},
	}
	if strings.TrimSpace(hardcoverToken) != "" {
		settings.Set("settings[hardcover_enabled]", "true")
		settings.Set("settings[hardcover_api_token]", strings.TrimSpace(hardcoverToken))
	}
	status, _, body, err := c.request(ctx, http.MethodPatch, settingsPath+"/bulk_update", csrf, settings)
	if err != nil {
		return err
	}
	// The authenticated bulk form compares the actual stored values, even for
	// masked secrets, and skips unchanged settings. An HTML redirect can also
	// represent validation failure, so require the Turbo success response.
	if status != http.StatusOK || !strings.Contains(body, "Settings updated successfully.") {
		return fmt.Errorf("Shelfarr did not confirm saving settings (HTTP %d)", status)
	}
	return nil
}

func (c *Client) signIn(ctx context.Context, password string) error {
	csrf, err := c.formToken(ctx, "/session/new?local=1")
	if err != nil {
		return err
	}
	status, _, _, err := c.request(ctx, http.MethodPost, "/session", csrf, url.Values{
		"username": {"household"}, "password": {password},
	})
	if err != nil {
		return err
	}
	if status != http.StatusFound && status != http.StatusSeeOther {
		return fmt.Errorf("Shelfarr household sign-in returned HTTP %d", status)
	}
	return nil
}

func (c *Client) formToken(ctx context.Context, path string) (string, error) {
	status, _, body, err := c.request(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("Shelfarr form %s returned HTTP %d", path, status)
	}
	tokens := html.NewTokenizer(strings.NewReader(body))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return "", fmt.Errorf("Shelfarr form %s did not provide a CSRF token", path)
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.Data != "meta" {
				continue
			}
			var name, content string
			for _, attr := range token.Attr {
				switch attr.Key {
				case "name":
					name = attr.Val
				case "content":
					content = attr.Val
				}
			}
			if name == "csrf-token" && content != "" {
				return content, nil
			}
		}
	}
}

func (c *Client) request(ctx context.Context, method, path, csrf string, form url.Values) (int, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", "", fmt.Errorf("build Shelfarr %s %s request", method, path)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Origin", c.base)
	}
	if method == http.MethodPatch {
		req.Header.Set("Accept", "text/vnd.turbo-stream.html")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, "", "", fmt.Errorf("Shelfarr %s %s request failed: %w", method, path, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return 0, "", "", fmt.Errorf("read Shelfarr %s %s response", method, path)
	}
	return res.StatusCode, res.Header.Get("Location"), string(body), nil
}

func hasPath(raw, path string) bool {
	location, err := url.Parse(raw)
	return err == nil && location.Path == path
}
