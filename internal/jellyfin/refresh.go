package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	movieLibraryName  = "Movie Library"
	seriesLibraryName = "Series Library"
	refreshKeyPrefix  = "Media Stack Library Refresh ("
)

type scanTrigger struct {
	Type            string `json:"Type"`
	TimeOfDayTicks  int64  `json:"TimeOfDayTicks"`
	MaxRuntimeTicks *int64 `json:"MaxRuntimeTicks,omitempty"`
}

// ReconcileRefreshPolicy configures existing libraries and the built-in scan task.
// Before changing options, prepareMonitorChange records that the server needs to reload its watchers.
func (client *Client) ReconcileRefreshPolicy(ctx context.Context, credentials Credentials, scanMinutes int, prepareMonitorChange func() error) error {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return err
	}
	if err := client.reconcileLibraryMonitors(ctx, auth.AccessToken, prepareMonitorChange); err != nil {
		return err
	}
	var tasks []struct {
		Key      string        `json:"Key"`
		ID       string        `json:"Id"`
		Triggers []scanTrigger `json:"Triggers"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/ScheduledTasks", nil, auth.AccessToken, &tasks); err != nil {
		return fmt.Errorf("observe Jellyfin recovery scan task: %w", err)
	}
	desired := scanTrigger{Type: "DailyTrigger", TimeOfDayTicks: int64(time.Duration(scanMinutes)*time.Minute) / 100}
	for _, task := range tasks {
		if task.Key != "RefreshLibrary" || task.ID == "" {
			continue
		}
		if len(task.Triggers) == 1 && task.Triggers[0] == desired {
			return nil
		}
		if err := client.doJSON(ctx, http.MethodPost, "/ScheduledTasks/"+url.PathEscape(task.ID)+"/Triggers", []scanTrigger{desired}, auth.AccessToken, nil); err != nil {
			return fmt.Errorf("set Jellyfin daily recovery scan: %w", err)
		}
		return nil
	}
	return fmt.Errorf("Jellyfin omitted its built-in RefreshLibrary scheduled task")
}

func (client *Client) reconcileLibraryMonitors(ctx context.Context, token string, prepareMonitorChange func() error) error {
	type library struct {
		Name           string                     `json:"Name"`
		ID             string                     `json:"ItemId"`
		LibraryOptions map[string]json.RawMessage `json:"LibraryOptions"`
	}
	var libraries []library
	if err := client.doJSON(ctx, http.MethodGet, "/Library/VirtualFolders", nil, token, &libraries); err != nil {
		return fmt.Errorf("observe Jellyfin library monitoring: %w", err)
	}
	indexed := make(map[string]library, len(libraries))
	for _, current := range libraries {
		indexed[current.Name] = current
	}
	var updates []library
	for _, name := range []string{movieLibraryName, seriesLibraryName} {
		current := indexed[name]
		if current.LibraryOptions == nil {
			return fmt.Errorf("Jellyfin omitted the %s options", name)
		}
		if string(current.LibraryOptions["EnableRealtimeMonitor"]) != "true" {
			if current.ID == "" {
				return fmt.Errorf("Jellyfin omitted the %s item identity", name)
			}
			current.LibraryOptions["EnableRealtimeMonitor"] = json.RawMessage("true")
			updates = append(updates, current)
		}
	}
	if len(updates) == 0 {
		return nil
	}
	if err := prepareMonitorChange(); err != nil {
		return err
	}
	for _, current := range updates {
		body := map[string]any{"Id": current.ID, "LibraryOptions": current.LibraryOptions}
		if err := client.doJSON(ctx, http.MethodPost, "/Library/VirtualFolders/LibraryOptions", body, token, nil); err != nil {
			return fmt.Errorf("enable Jellyfin real-time monitoring for %s: %w", current.Name, err)
		}
	}
	return nil
}

// ReconcileRefreshAPIKey returns a key for this Jellyfin server, recovering a key
// issued by a previous apply even if writing the Environment secret was interrupted.
func (client *Client) ReconcileRefreshAPIKey(ctx context.Context, credentials Credentials, project, stored string) (string, error) {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return "", err
	}
	app := refreshKeyPrefix + project + ")"
	key, err := client.refreshAPIKey(ctx, auth.AccessToken, app, stored)
	if err != nil || key != "" {
		return key, err
	}
	if err := client.doJSON(ctx, http.MethodPost, "/Auth/Keys?app="+url.QueryEscape(app), nil, auth.AccessToken, nil); err != nil {
		return "", fmt.Errorf("create Jellyfin library refresh API key: %w", err)
	}
	key, err = client.refreshAPIKey(ctx, auth.AccessToken, app, "")
	if err == nil && key == "" {
		err = fmt.Errorf("Jellyfin did not return the library refresh API key it created")
	}
	return key, err
}

type apiKey struct {
	AppName     string `json:"AppName"`
	AccessToken string `json:"AccessToken"`
}

func (client *Client) refreshAPIKey(ctx context.Context, token, app, stored string) (string, error) {
	keys, err := client.apiKeys(ctx, token)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, key := range keys {
		if key.AppName != app || key.AccessToken == "" {
			continue
		}
		if key.AccessToken == stored {
			return stored, nil
		}
		candidates = append(candidates, key.AccessToken)
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("multiple Jellyfin API keys are named %q; keep one and rerun apply", app)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", nil
}

func (client *Client) apiKeys(ctx context.Context, token string) ([]apiKey, error) {
	var keys struct {
		Items []apiKey `json:"Items"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/Auth/Keys", nil, token, &keys); err != nil {
		return nil, fmt.Errorf("observe Jellyfin API keys: %w", err)
	}
	return keys.Items, nil
}

// RemoveForeignRefreshAPIKeys removes cloned connector credentials from recovered Jellyfin state.
func (client *Client) RemoveForeignRefreshAPIKeys(ctx context.Context, credentials Credentials, project string) error {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return err
	}
	keys, err := client.apiKeys(ctx, auth.AccessToken)
	if err != nil {
		return err
	}
	local := refreshKeyPrefix + project + ")"
	for _, key := range keys {
		if !strings.HasPrefix(key.AppName, refreshKeyPrefix) || key.AppName == local || key.AccessToken == "" {
			continue
		}
		if err := client.doJSON(ctx, http.MethodDelete, "/Auth/Keys/"+url.PathEscape(key.AccessToken), nil, auth.AccessToken, nil); err != nil {
			return fmt.Errorf("revoke cloned Jellyfin refresh API key: %s", strings.ReplaceAll(err.Error(), key.AccessToken, "[redacted]"))
		}
	}
	return nil
}
