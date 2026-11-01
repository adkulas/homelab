package companions

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"golang.org/x/net/html"
)

type houndarrInstance struct {
	id      string
	enabled bool
	fields  url.Values
}

func (client *Houndarr) login(ctx context.Context, credentials AdminCredentials) error {
	_, _, err := client.page(ctx, "POST", "/login", url.Values{"username": {credentials.Username}, "password": {credentials.Password}})
	return err
}

func (client *Houndarr) instances(ctx context.Context) ([]houndarrInstance, error) {
	page, _, err := client.page(ctx, "GET", "/settings", nil)
	if err != nil {
		return nil, err
	}
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check Houndarr's settings page.")
	}
	client.csrf = formValues(root).Get("csrf_token")
	if client.csrf == "" {
		return nil, fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check the pinned Houndarr settings page for its CSRF token.")
	}
	var instances []houndarrInstance
	walk(root, func(node *html.Node) {
		if node.Data != "tr" || !strings.HasPrefix(attribute(node, "id"), "instance-row-") {
			return
		}
		instance := houndarrInstance{id: strings.TrimPrefix(attribute(node, "id"), "instance-row-")}
		walk(node, func(child *html.Node) {
			if child.Data == "button" && attribute(child, "hx-post") == "/settings/instances/"+instance.id+"/toggle-enabled" {
				instance.enabled = strings.TrimSpace(nodeText(child)) == "Disable"
			}
		})
		instances = append(instances, instance)
	})
	for _, instance := range instances {
		number, err := strconv.Atoi(instance.id)
		if err != nil || number <= 0 {
			return nil, fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check Houndarr's instance identifier.")
		}
	}
	return instances, nil
}

func (client *Houndarr) instanceSettings(ctx context.Context, instance houndarrInstance) (houndarrInstance, error) {
	page, _, err := client.page(ctx, "GET", "/settings/instances/"+instance.id+"/edit", nil)
	if err != nil {
		return instance, err
	}
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return instance, fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check Houndarr's instance editor.")
	}
	instance.fields = formValues(root)
	return instance, nil
}

func (client *Houndarr) testConnection(ctx context.Context, apiKey, id string) error {
	fields := url.Values{"type": {"sonarr"}, "url": {"http://sonarr:8989"}, "api_key": {apiKey}}
	if id != "" {
		fields.Set("instance_id", id)
	}
	_, headers, err := client.page(ctx, "POST", "/settings/instances/test-connection", fields)
	if err != nil {
		return err
	}
	if !strings.Contains(headers.Get("HX-Trigger"), "houndarr-connection-test-success") {
		return fail("VERIFY_COMPANION_TARGET_CREDENTIALS_INVALID", "Check Houndarr's stored Sonarr API key and test the Environment-local connection in its UI.")
	}
	return nil
}

func (client *Houndarr) verifyInstance(ctx context.Context, instance houndarrInstance, policy config.SeriesAutomation) error {
	if instance.enabled != policy.Enabled {
		return fail("VERIFY_DUPLICATE_PROACTIVE_OWNER", "Set the Houndarr instance enabled state to match this Environment's seriesAutomation.enabled policy.")
	}
	current := instance.fields
	if current.Get("type") != "sonarr" || strings.TrimRight(current.Get("url"), "/") != "http://sonarr:8989" {
		return fail("VERIFY_COMPANION_TARGET_INVALID", "Configure Houndarr for http://sonarr:8989 on this Environment's application network.")
	}
	if current.Get("sonarr_search_mode") != "episode" {
		return fail("VERIFY_EPISODE_SEARCH_CONTEXT_INVALID", "Select Episode search for Houndarr's Sonarr missing pass; rerun apply after guided correction.")
	}
	for name, expected := range HoundarrFields(policy) {
		actual, exists := current[name]
		if !exists || len(actual) != 1 || actual[0] != expected {
			return fail("VERIFY_COMPANION_POLICY_DRIFT", fmt.Sprintf("Set Houndarr instance field %s to %q through its UI, then rerun apply. See the series automation runbook.", name, expected))
		}
	}
	if policy.Enabled {
		return client.testConnection(ctx, "__UNCHANGED__", instance.id)
	}
	return nil
}
