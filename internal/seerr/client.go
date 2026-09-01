package seerr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"strings"
)

const (
	administratorPermission = 2
	requestPermission       = 32
)

type Credentials struct {
	Username string
	Password string
}

type Client struct {
	baseURL string
	http    *http.Client
}

type user struct {
	ID          int    `json:"id"`
	Email       string `json:"email"`
	Permissions int    `json:"permissions"`
}

type mainSettings struct {
	LocalLogin         bool `json:"localLogin"`
	MediaServerLogin   bool `json:"mediaServerLogin"`
	NewJellyfinLogin   bool `json:"newPlexLogin"`
	DefaultPermissions int  `json:"defaultPermissions"`
}

type radarrDestination struct {
	ID                  int    `json:"id,omitempty"`
	Name                string `json:"name"`
	Hostname            string `json:"hostname"`
	Port                int    `json:"port"`
	APIKey              string `json:"apiKey"`
	UseSSL              bool   `json:"useSsl"`
	BaseURL             string `json:"baseUrl"`
	ActiveProfileID     int    `json:"activeProfileId"`
	ActiveProfileName   string `json:"activeProfileName"`
	ActiveDirectory     string `json:"activeDirectory"`
	Tags                []int  `json:"tags"`
	Is4K                bool   `json:"is4k"`
	IsDefault           bool   `json:"isDefault"`
	MinimumAvailability string `json:"minimumAvailability"`
	ExternalURL         string `json:"externalUrl"`
	SyncEnabled         bool   `json:"syncEnabled"`
	PreventSearch       bool   `json:"preventSearch"`
	TagRequests         bool   `json:"tagRequests"`
	OverrideRule        []int  `json:"overrideRule"`
}

type radarrTestResponse struct {
	Profiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"profiles"`
	RootFolders []struct {
		Path string `json:"path"`
	} `json:"rootFolders"`
}

func internalRadarrDestination(apiKey string) radarrDestination {
	return radarrDestination{Hostname: "radarr", Port: 7878, APIKey: apiKey, UseSSL: false, BaseURL: ""}
}

func (destination radarrDestination) hasSameEndpoint(other radarrDestination) bool {
	return destination.Hostname == other.Hostname &&
		destination.Port == other.Port &&
		destination.UseSSL == other.UseSSL &&
		destination.BaseURL == other.BaseURL
}

func New(baseURL string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if httpClient.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("create Seerr session: %w", err)
		}
		httpClient.Jar = jar
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}, nil
}

func (client *Client) ReconcileAuthentication(ctx context.Context, credentials Credentials) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fmt.Errorf("Seerr authentication requires Jellyfin administrator credentials")
	}
	var public struct {
		Initialized     bool `json:"initialized"`
		MediaServerType int  `json:"mediaServerType"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/settings/public", nil, &public); err != nil {
		return fmt.Errorf("observe Seerr startup: %w", err)
	}
	owner, err := client.authenticateJellyfin(ctx, credentials, public.MediaServerType == 4)
	if err != nil {
		return err
	}

	var main mainSettings
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/settings/main", nil, &main); err != nil {
		return fmt.Errorf("observe Seerr authentication policy: %w", err)
	}
	if !main.LocalLogin || !main.MediaServerLogin || !main.NewJellyfinLogin || main.DefaultPermissions != requestPermission {
		main.LocalLogin = true
		main.MediaServerLogin = true
		main.NewJellyfinLogin = true
		main.DefaultPermissions = requestPermission
		if err := client.doJSON(ctx, http.MethodPost, "/api/v1/settings/main", main, nil); err != nil {
			return fmt.Errorf("reconcile Seerr authentication policy: %w", err)
		}
	}

	if err := client.VerifyLocalAdministrator(ctx, credentials); err != nil {
		owner, err = client.authenticateJellyfin(ctx, credentials, false)
		if err != nil {
			return err
		}
		password := map[string]string{"newPassword": credentials.Password}
		path := fmt.Sprintf("/api/v1/user/%d/settings/password", owner.ID)
		if err := client.doJSON(ctx, http.MethodPost, path, password, nil); err != nil {
			return fmt.Errorf("establish Seerr emergency local administrator: %w", err)
		}
		if err := client.VerifyLocalAdministrator(ctx, credentials); err != nil {
			return err
		}
	}
	if !public.Initialized {
		if err := client.doJSON(ctx, http.MethodPost, "/api/v1/settings/initialize", map[string]any{}, nil); err != nil {
			return fmt.Errorf("complete Seerr initialization: %w", err)
		}
	}
	return nil
}

func (client *Client) VerifyJellyfinAuthentication(ctx context.Context, credentials Credentials) error {
	if _, err := client.authenticateJellyfin(ctx, credentials, false); err != nil {
		return fmt.Errorf("verify household Seerr authentication through Jellyfin: %w", err)
	}
	return nil
}

func (client *Client) VerifyLocalAdministrator(ctx context.Context, credentials Credentials) error {
	localSession, err := client.authenticateLocal(ctx, credentials)
	if err != nil {
		return fmt.Errorf("verify Seerr emergency local administrator: %w", err)
	}
	var current user
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/auth/me", nil, &current); err != nil {
		return fmt.Errorf("verify Seerr emergency local administrator permission: %w", err)
	}
	if current.ID != localSession.ID {
		return fmt.Errorf("verify Seerr emergency local administrator: session user changed from %d to %d", localSession.ID, current.ID)
	}
	if current.Permissions&administratorPermission == 0 {
		return fmt.Errorf("verify Seerr emergency local administrator: user %d lacks administrator permission", current.ID)
	}
	return nil
}

func (client *Client) RequestMovie(ctx context.Context, credentials Credentials, tmdbID int) error {
	if tmdbID <= 0 {
		return fmt.Errorf("request movie through Seerr: TMDB identity must be positive")
	}
	if _, err := client.authenticateJellyfin(ctx, credentials, false); err != nil {
		return fmt.Errorf("request movie through Seerr: %w", err)
	}
	payload := map[string]any{"mediaType": "movie", "mediaId": tmdbID, "is4k": false}
	var created struct {
		ID     int `json:"id"`
		Status int `json:"status"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/request", payload, &created); err != nil {
		return fmt.Errorf("request movie through Seerr: %w", err)
	}
	if created.ID == 0 || created.Status != 2 {
		return fmt.Errorf("request movie through Seerr: response did not contain an approved request")
	}
	return nil
}

func (client *Client) ReconcileRadarr(ctx context.Context, apiKey, profileName string) error {
	connection := internalRadarrDestination(apiKey)
	var tested radarrTestResponse
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/settings/radarr/test", connection, &tested); err != nil {
		return fmt.Errorf("test Seerr Radarr destination: %w", err)
	}
	profileID := 0
	profileMatches := 0
	for _, profile := range tested.Profiles {
		if profile.Name != profileName {
			continue
		}
		profileMatches++
		profileID = profile.ID
	}
	if profileMatches == 0 || profileID <= 0 {
		return fmt.Errorf("reconcile Seerr Radarr destination: Radarr profile %q is missing", profileName)
	}
	if profileMatches > 1 {
		return fmt.Errorf("reconcile Seerr Radarr destination: Radarr returned duplicate %q profiles", profileName)
	}
	rootFound := false
	for _, root := range tested.RootFolders {
		if root.Path == "/data/media/movies" {
			rootFound = true
			break
		}
	}
	if !rootFound {
		return fmt.Errorf("reconcile Seerr Radarr destination: Movie Library root is missing")
	}
	desired := internalRadarrDestination(apiKey)
	desired.Name = "Radarr"
	desired.ActiveProfileID, desired.ActiveProfileName, desired.ActiveDirectory = profileID, profileName, "/data/media/movies"
	desired.Tags, desired.Is4K, desired.IsDefault = []int{}, false, true
	desired.MinimumAvailability, desired.ExternalURL, desired.SyncEnabled = "released", "", true
	desired.PreventSearch, desired.TagRequests, desired.OverrideRule = false, false, []int{}
	var destinations []radarrDestination
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/settings/radarr", nil, &destinations); err != nil {
		return fmt.Errorf("observe Seerr Radarr destination: %w", err)
	}
	match := -1
	for index, destination := range destinations {
		if destination.hasSameEndpoint(desired) {
			if match != -1 {
				return fmt.Errorf("reconcile Seerr Radarr destination: multiple internal Radarr destinations exist")
			}
			match = index
		}
	}
	if match == -1 {
		if err := client.doJSON(ctx, http.MethodPost, "/api/v1/settings/radarr", desired, nil); err != nil {
			return fmt.Errorf("create Seerr Radarr destination: %w", err)
		}
		return nil
	}
	desired.ID = destinations[match].ID
	if reflect.DeepEqual(destinations[match], desired) {
		return nil
	}
	path := fmt.Sprintf("/api/v1/settings/radarr/%d", desired.ID)
	if err := client.doJSON(ctx, http.MethodPut, path, desired, nil); err != nil {
		return fmt.Errorf("repair Seerr Radarr destination: %w", err)
	}
	return nil
}

func (client *Client) authenticateJellyfin(ctx context.Context, credentials Credentials, bootstrap bool) (user, error) {
	payload := map[string]any{
		"username":   credentials.Username,
		"password":   credentials.Password,
		"email":      credentials.Username,
		"serverType": 2,
	}
	if bootstrap {
		payload["hostname"] = "jellyfin"
		payload["port"] = 8096
		payload["urlBase"] = ""
		payload["useSsl"] = false
	}
	var authenticated user
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/auth/jellyfin", payload, &authenticated); err != nil {
		return authenticated, fmt.Errorf("authenticate Seerr through Jellyfin: %w", err)
	}
	if authenticated.ID == 0 {
		return authenticated, fmt.Errorf("authenticate Seerr through Jellyfin: response omitted user identity")
	}
	return authenticated, nil
}

func (client *Client) authenticateLocal(ctx context.Context, credentials Credentials) (user, error) {
	var authenticated user
	payload := map[string]string{"email": credentials.Username, "password": credentials.Password}
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/auth/local", payload, &authenticated); err != nil {
		return authenticated, err
	}
	if authenticated.ID == 0 {
		return authenticated, fmt.Errorf("response omitted user identity")
	}
	return authenticated, nil
}

func (client *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(contents)))
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			return err
		}
	}
	return nil
}
