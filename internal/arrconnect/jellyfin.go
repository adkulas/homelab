package arrconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const jellyfinConnectionName = "Media Stack Jellyfin Library Refresh"

type jellyfinConnection struct {
	ID                 int             `json:"id,omitempty"`
	Name               string          `json:"name"`
	Implementation     string          `json:"implementation"`
	ImplementationName string          `json:"implementationName"`
	ConfigContract     string          `json:"configContract"`
	Tags               []int           `json:"tags"`
	Fields             []jellyfinField `json:"fields"`
}

type jellyfinField struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// ReconcileJellyfin uses the common Radarr and Sonarr notification API.
func ReconcileJellyfin(ctx context.Context, key string, triggers []string, get func(context.Context, string, any) error, send func(context.Context, string, string, any, ...int) error) error {
	if key == "" {
		return fmt.Errorf("Jellyfin library refresh API key is required")
	}
	const endpoint = "/api/v3/notification"
	var connections []map[string]json.RawMessage
	if err := get(ctx, endpoint, &connections); err != nil {
		return fmt.Errorf("observe native Jellyfin connection: %s", strings.ReplaceAll(err.Error(), key, "[redacted]"))
	}
	var existing map[string]json.RawMessage
	var current jellyfinConnection
	for _, connection := range connections {
		var name string
		_ = json.Unmarshal(connection["name"], &name)
		if name != jellyfinConnectionName {
			continue
		}
		if existing != nil {
			return fmt.Errorf("multiple connections named %q", jellyfinConnectionName)
		}
		existing = connection
	}
	if existing != nil {
		encoded, _ := json.Marshal(existing)
		if err := json.Unmarshal(encoded, &current); err != nil || current.ID <= 0 || current.Implementation != "MediaBrowser" {
			return fmt.Errorf("connection named %q is not a valid native Jellyfin connection", jellyfinConnectionName)
		}
	}

	desired := jellyfinConnection{ID: current.ID, Name: jellyfinConnectionName, Implementation: "MediaBrowser", ImplementationName: "Emby / Jellyfin", ConfigContract: "MediaBrowserSettings", Tags: []int{}}
	for _, item := range []struct {
		name  string
		value any
	}{
		{"host", "jellyfin"}, {"port", 8096}, {"useSsl", false}, {"urlBase", ""}, {"apiKey", key},
		{"notify", false}, {"updateLibrary", true}, {"mapFrom", ""}, {"mapTo", ""},
	} {
		encoded, _ := json.Marshal(item.value)
		desired.Fields = append(desired.Fields, jellyfinField{Name: item.name, Value: encoded})
	}
	enabled := map[string]bool{"onDownload": true, "onUpgrade": true, "onRename": true}
	for _, trigger := range triggers {
		enabled[trigger] = true
	}
	if existing != nil && jellyfinConnectionMatches(current, desired, existing, enabled) {
		return nil
	}
	encoded, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("encode native Jellyfin connection: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return fmt.Errorf("encode native Jellyfin triggers: %w", err)
	}
	for trigger := range enabled {
		payload[trigger] = true
	}
	method, path := http.MethodPost, endpoint
	if current.ID != 0 {
		method, path = http.MethodPut, fmt.Sprintf("%s/%d", endpoint, current.ID)
	}
	if err := send(ctx, method, path, payload, http.StatusAccepted); err != nil {
		return fmt.Errorf("reconcile native Jellyfin connection: %s", strings.ReplaceAll(err.Error(), key, "[redacted]"))
	}
	return nil
}

func jellyfinConnectionMatches(current, desired jellyfinConnection, raw map[string]json.RawMessage, enabled map[string]bool) bool {
	if current.ConfigContract != desired.ConfigContract || len(current.Tags) != 0 {
		return false
	}
	for trigger := range enabled {
		if string(raw[trigger]) != "true" {
			return false
		}
	}
	for field, value := range raw {
		if strings.HasPrefix(field, "on") && string(value) == "true" && !enabled[field] {
			return false
		}
	}
	fields := make(map[string]string, len(current.Fields))
	for _, field := range current.Fields {
		fields[field.Name] = string(field.Value)
	}
	for _, field := range desired.Fields {
		observed := fields[field.Name]
		if string(field.Value) == `""` && (observed == "" || observed == "null") {
			continue
		}
		// The pinned APIs mask the key. A PUT with the declared key lets Arr
		// compare it to its saved credential and write only if it differs.
		if observed != string(field.Value) {
			return false
		}
	}
	return true
}
