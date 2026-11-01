package companions

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

// Bootstrap uses the supported setup API and never resets an existing account.
// The caller must persist the returned API key before enabling queue recovery.
func (client *Cleanuparr) Bootstrap(ctx context.Context, credentials AdminCredentials) (string, error) {
	if err := ValidateAdminCredentials("Cleanuparr", credentials); err != nil {
		return "", err
	}
	var status struct {
		SetupCompleted bool `json:"setupCompleted"`
	}
	if err := client.api.request(ctx, "GET", "/api/auth/status", nil, &status); err != nil {
		return "", err
	}
	if !status.SetupCompleted {
		err := client.api.request(ctx, "POST", "/api/auth/setup/account", document{"username": credentials.Username, "password": credentials.Password}, nil)
		var failure *Failure
		if err != nil && !(errors.As(err, &failure) && failure.status == 409) {
			return "", err
		}
		if err := client.api.request(ctx, "POST", "/api/auth/setup/complete", document{}, nil); err != nil {
			if !errors.As(err, &failure) || failure.status != 409 {
				return "", err
			}
		}
	}
	var login struct {
		RequiresTwoFactor bool `json:"requiresTwoFactor"`
		Tokens            struct {
			AccessToken string `json:"accessToken"`
		} `json:"tokens"`
	}
	if err := client.api.request(ctx, "POST", "/api/auth/login", document{"username": credentials.Username, "password": credentials.Password}, &login); err != nil {
		return "", err
	}
	if login.RequiresTwoFactor || login.Tokens.AccessToken == "" {
		return "", fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Use an existing Cleanuparr API key for an account protected by two-factor authentication; store cleanuparr.apiKey in the selected Environment SOPS document.")
	}
	authenticated := client.api
	authenticated.bearer = login.Tokens.AccessToken
	var result struct {
		APIKey string `json:"apiKey"`
	}
	if err := authenticated.request(ctx, "GET", "/api/account/api-key", nil, &result); err != nil {
		return "", err
	}
	if result.APIKey == "" {
		return "", fail("VERIFY_COMPANION_RESPONSE_INVALID", "Cleanuparr returned an empty API key.")
	}
	return result.APIKey, nil
}

// Bootstrap creates a first-run account and one local Sonarr connection. Searches
// stay paused until the saved settings have passed observation and connection testing.
func (client *Houndarr) Bootstrap(ctx context.Context, credentials AdminCredentials, policy config.SeriesAutomation, sonarrKey string) error {
	page, headers, err := client.page(ctx, "GET", "/setup", nil)
	if err != nil {
		return err
	}
	if headers.Get("Location") != "/login" {
		if !strings.Contains(page, "password_confirm") {
			return fail("VERIFY_COMPANION_RESPONSE_INVALID", "Check the pinned Houndarr setup form.")
		}
		if err := ValidateAdminCredentials("Houndarr", credentials); err != nil {
			return err
		}
		if _, _, err := client.page(ctx, "POST", "/setup", url.Values{"username": {credentials.Username}, "password": {credentials.Password}, "password_confirm": {credentials.Password}}); err != nil {
			return err
		}
	}
	if err := client.login(ctx, credentials); err != nil {
		return err
	}
	instances, err := client.instances(ctx)
	if err != nil {
		return err
	}
	if len(instances) > 1 {
		return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Keep exactly one Environment-local Sonarr connection in Houndarr.")
	}
	fields := url.Values{"name": {"media-stack Sonarr"}, "type": {"sonarr"}, "url": {"http://sonarr:8989"}, "api_key": {sonarrKey}, "sonarr_search_mode": {"episode"}, "connection_verified": {"true"}}
	for key, value := range HoundarrFields(policy) {
		fields.Set(key, value)
	}
	if len(instances) == 0 {
		fields.Set("missing_enabled", "")
		if err := client.testConnection(ctx, sonarrKey, ""); err != nil {
			return err
		}
		if _, _, err := client.page(ctx, "POST", "/settings/instances", fields); err != nil {
			return err
		}
		instances, err = client.instances(ctx)
		if err != nil {
			return err
		}
		if len(instances) != 1 {
			return fail("VERIFY_COMPANION_BOOTSTRAP_REQUIRED", "Houndarr did not confirm creation of one paused local Sonarr connection; inspect its UI before retrying.")
		}
	}
	instance, err := client.instanceSettings(ctx, instances[0])
	if err != nil {
		return err
	}
	verificationErr := client.verifyInstance(ctx, instance, policy)
	if verificationErr == nil {
		return nil
	}
	// Resume only this workflow's paused local connection. Configured connections
	// remain observed rather than silently overwritten.
	current := instance.fields
	if current.Get("name") != "media-stack Sonarr" || current.Get("type") != "sonarr" || current.Get("url") != "http://sonarr:8989" || (instance.enabled && current.Get("missing_enabled") == "on") {
		return verificationErr
	}
	id := instance.id
	if instance.enabled {
		if _, _, err := client.page(ctx, "POST", "/settings/instances/"+id+"/toggle-enabled", url.Values{}); err != nil {
			return err
		}
	}
	fields.Set("missing_enabled", "on")
	if _, _, err := client.page(ctx, "POST", "/settings/instances/"+id, fields); err != nil {
		return err
	}
	paused := policy
	paused.Enabled = false
	if err := client.Verify(ctx, credentials, paused); err != nil {
		return err
	}
	if err := client.testConnection(ctx, "__UNCHANGED__", id); err != nil {
		return err
	}
	if policy.Enabled {
		if _, _, err := client.page(ctx, "POST", "/settings/instances/"+id+"/toggle-enabled", url.Values{}); err != nil {
			return err
		}
	}
	return client.Verify(ctx, credentials, policy)
}
