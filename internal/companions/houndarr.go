package companions

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"golang.org/x/net/html"
)

// AdminCredentials authenticates read-only settings observation. Initial account
// creation and policy changes remain guided through the upstream web interface.
type AdminCredentials struct{ Username, Password string }

type Houndarr struct {
	api  api
	csrf string
}

func NewHoundarr(base string, client *http.Client) *Houndarr {
	transport := newAPI(base, "", client)
	transport.http.Jar, _ = cookiejar.New(nil)
	return &Houndarr{api: transport}
}

func (client *Houndarr) Verify(ctx context.Context, credentials AdminCredentials, policy config.SeriesAutomation) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Create the selected Environment Houndarr account and store houndarr.username and houndarr.password in its SOPS document; configure one local Sonarr instance in the UI.")
	}
	_, _, err := client.page(ctx, "POST", "/login", url.Values{"username": {credentials.Username}, "password": {credentials.Password}})
	if err != nil {
		return err
	}
	page, _, err := client.page(ctx, "GET", "/settings", nil)
	if err != nil {
		return err
	}
	nodes, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check the pinned Houndarr version and its settings page.")
	}
	var rows []*html.Node
	walk(nodes, func(node *html.Node) {
		if node.Data == "tr" && strings.HasPrefix(attribute(node, "id"), "instance-row-") {
			rows = append(rows, node)
		}
	})
	values := formValues(nodes)
	client.csrf = values.Get("csrf_token")
	if len(rows) != 1 {
		return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Keep exactly one local Sonarr instance in this Environment's Houndarr UI; other connections can create overlapping searches.")
	}
	row := rows[0]
	id := strings.TrimPrefix(attribute(row, "id"), "instance-row-")
	if _, err := strconv.Atoi(id); err != nil {
		return fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check the pinned Houndarr settings page.")
	}
	enabled := false
	walk(row, func(node *html.Node) {
		if node.Data == "button" && strings.HasSuffix(attribute(node, "hx-post"), "/toggle-enabled") {
			enabled = strings.TrimSpace(nodeText(node)) == "Disable"
		}
	})
	if enabled != policy.Enabled {
		return fail("VERIFY_DUPLICATE_PROACTIVE_OWNER", "Set the Houndarr instance enabled state to match this Environment's seriesAutomation.enabled policy.")
	}
	form, _, err := client.page(ctx, "GET", "/settings/instances/"+id+"/edit", nil)
	if err != nil {
		return err
	}
	fields, err := html.Parse(strings.NewReader(form))
	if err != nil {
		return fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check Houndarr's instance editor.")
	}
	current := formValues(fields)
	if current.Get("type") != "sonarr" || strings.TrimRight(current.Get("url"), "/") != "http://sonarr:8989" {
		return fail("VERIFY_COMPANION_TARGET_INVALID", "Configure Houndarr for http://sonarr:8989 on this Environment's application network.")
	}
	if current.Get("sonarr_search_mode") != "episode" {
		return fail("VERIFY_EPISODE_SEARCH_CONTEXT_INVALID", "Select Episode search for Houndarr's Sonarr missing pass; rerun apply after guided correction.")
	}
	desired := HoundarrFields(policy)
	for name, expected := range desired {
		actual, exists := current[name]
		if !exists || len(actual) != 1 || actual[0] != expected {
			return fail("VERIFY_COMPANION_POLICY_DRIFT", fmt.Sprintf("Set Houndarr instance field %s to %q through its UI, then rerun apply. See the series automation runbook.", name, expected))
		}
	}
	if policy.Enabled {
		_, headers, err := client.page(ctx, "POST", "/settings/instances/test-connection", url.Values{"type": {"sonarr"}, "url": {"http://sonarr:8989"}, "api_key": {"__UNCHANGED__"}, "instance_id": {id}})
		if err != nil {
			return err
		}
		if !strings.Contains(headers.Get("HX-Trigger"), "houndarr-connection-test-success") {
			return fail("VERIFY_COMPANION_TARGET_CREDENTIALS_INVALID", "Update Houndarr's stored Sonarr API key and test the Environment-local connection in its UI.")
		}
	}
	return nil
}

// HoundarrFields supplies the exact upstream form settings for guided bootstrap.
func HoundarrFields(policy config.SeriesAutomation) map[string]string {
	s := policy.Search
	return map[string]string{"batch_size": strconv.Itoa(s.BatchSize), "sleep_interval_mins": strconv.Itoa(s.IntervalMinutes), "hourly_cap": strconv.Itoa(s.HourlyCap), "cooldown_days": strconv.Itoa(s.CooldownDays), "post_release_grace_hrs": strconv.Itoa(s.ReleaseGraceHours), "allowed_time_window": s.AllowedWindow, "queue_limit": strconv.Itoa(s.QueueLimit), "search_order": "chronological", "missing_hot_retry_window_hrs": "0", "missing_enabled": "on", "cutoff_enabled": "", "upgrade_enabled": "", "tag_filter_include": "", "tag_filter_exclude": ""}
}

func (client *Houndarr) page(ctx context.Context, method, path string, form url.Values) (string, http.Header, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, client.api.base+path, body)
	if err != nil {
		return "", nil, fail("VERIFY_COMPANION_UNAVAILABLE", "Check Houndarr's management endpoint.")
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if client.csrf != "" {
		request.Header.Set("X-CSRF-Token", client.csrf)
	}
	response, err := client.api.http.Do(request)
	if err != nil {
		return "", nil, &Failure{Code: "VERIFY_COMPANION_UNAVAILABLE", Remedy: "Start Houndarr in the selected Environment and retry.", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return "", nil, fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Correct Houndarr admin credentials or its CSRF-protected session.")
	}
	if method == "POST" && path == "/login" && response.StatusCode == 303 {
		return "", response.Header, nil
	}
	if response.StatusCode == 302 || response.StatusCode == 303 {
		return "", nil, fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Complete the Houndarr account setup through its loopback management endpoint.")
	}
	if response.StatusCode != 200 {
		return "", nil, fail("VERIFY_COMPANION_API_FAILED", fmt.Sprintf("Houndarr %s %s returned HTTP %d; inspect local service logs.", method, path, response.StatusCode))
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", nil, fail("VERIFY_COMPANION_RESPONSE_INVALID", "Retry Houndarr settings observation.")
	}
	return string(encoded), response.Header, nil
}

func walk(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}
func attribute(node *html.Node, name string) string {
	for _, item := range node.Attr {
		if item.Key == name {
			return item.Val
		}
	}
	return ""
}
func hasAttribute(node *html.Node, name string) bool {
	for _, item := range node.Attr {
		if item.Key == name {
			return true
		}
	}
	return false
}
func nodeText(node *html.Node) string {
	var out strings.Builder
	walk(node, func(child *html.Node) {
		if child.Type == html.TextNode {
			out.WriteString(child.Data)
		}
	})
	return out.String()
}
func formValues(root *html.Node) url.Values {
	values := url.Values{}
	walk(root, func(node *html.Node) {
		name := attribute(node, "name")
		if name == "" {
			return
		}
		switch node.Data {
		case "input":
			value := attribute(node, "value")
			if attribute(node, "type") == "checkbox" {
				value = ""
				if hasAttribute(node, "checked") {
					value = "on"
				}
			}
			values.Set(name, value)
		case "select":
			selected := ""
			walk(node, func(child *html.Node) {
				if child.Data == "option" && hasAttribute(child, "selected") {
					selected = attribute(child, "value")
				}
			})
			values.Set(name, selected)
		}
	})
	return values
}
