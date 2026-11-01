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

// AdminCredentials contains operator-selected credentials for setup and observation.
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
		return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Run media-stack init --companions for the selected Environment to encrypt chosen credentials, then run apply to configure Houndarr.")
	}
	if err := client.login(ctx, credentials); err != nil {
		return err
	}
	instances, err := client.instances(ctx)
	if err != nil {
		return err
	}
	if len(instances) != 1 {
		return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Keep exactly one local Sonarr instance in this Environment's Houndarr UI; run apply for first-run setup.")
	}
	instance, err := client.instanceSettings(ctx, instances[0])
	if err != nil {
		return err
	}
	return client.verifyInstance(ctx, instance, policy)
}

// HoundarrFields supplies the exact upstream form settings for first-run setup.
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
	if method == "POST" && path == "/login" && response.StatusCode == 200 {
		return "", nil, fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Correct the selected Environment Houndarr credentials; existing accounts are never reset.")
	}
	setupRedirect := path == "/setup" && response.Header.Get("Location") == "/login" && ((method == "GET" && response.StatusCode == 302) || (method == "POST" && response.StatusCode == 303))
	loginRedirect := method == "POST" && path == "/login" && response.StatusCode == 303 && response.Header.Get("Location") == "/"
	if setupRedirect || loginRedirect {
		return "", response.Header, nil
	}
	if response.StatusCode == 302 || response.StatusCode == 303 {
		return "", nil, fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Run init --companions and apply for the selected Environment to complete Houndarr setup.")
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
