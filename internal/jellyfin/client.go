package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	movieLibraryPath  = "/data/media/movies"
	seriesLibraryPath = "/data/media/series"
)

type Credentials struct {
	Username string
	Password string
}

type Client struct {
	baseURL string
	http    *http.Client
}

type authentication struct {
	AccessToken string `json:"AccessToken"`
	User        struct {
		ID     string         `json:"Id"`
		Policy map[string]any `json:"Policy"`
	} `json:"User"`
}

type user struct {
	ID     string         `json:"Id"`
	Policy map[string]any `json:"Policy"`
}

func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

func (client *Client) ReconcileLibraries(ctx context.Context, credentials Credentials) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fmt.Errorf("Jellyfin username and password are required")
	}
	var publicInfo struct {
		StartupWizardCompleted bool `json:"StartupWizardCompleted"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/System/Info/Public", nil, "", &publicInfo); err != nil {
		return fmt.Errorf("observe Jellyfin startup: %w", err)
	}
	if !publicInfo.StartupWizardCompleted {
		steps := []struct {
			method string
			path   string
			body   any
		}{
			{http.MethodPost, "/Startup/Configuration", map[string]any{"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en"}},
			{http.MethodGet, "/Startup/User", nil},
			{http.MethodPost, "/Startup/User", map[string]string{"Name": credentials.Username, "Password": credentials.Password}},
			{http.MethodPost, "/Startup/RemoteAccess", map[string]bool{"EnableRemoteAccess": false, "EnableAutomaticPortMapping": false}},
			{http.MethodPost, "/Startup/Complete", map[string]any{}},
		}
		for _, step := range steps {
			if err := client.doJSON(ctx, step.method, step.path, step.body, "", nil); err != nil {
				return fmt.Errorf("complete Jellyfin startup at %s: %w", step.path, err)
			}
		}
	}
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return err
	}
	var libraries []struct {
		Name           string   `json:"Name"`
		CollectionType string   `json:"CollectionType"`
		Locations      []string `json:"Locations"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/Library/VirtualFolders", nil, auth.AccessToken, &libraries); err != nil {
		return fmt.Errorf("observe Jellyfin libraries: %w", err)
	}
	desiredLibraries := []struct {
		name           string
		collectionType string
		path           string
	}{
		{name: "Movie Library", collectionType: "movies", path: movieLibraryPath},
		{name: "Series Library", collectionType: "tvshows", path: seriesLibraryPath},
	}
	for _, desired := range desiredLibraries {
		found := false
		for _, library := range libraries {
			if library.Name != desired.name {
				continue
			}
			if library.CollectionType != desired.collectionType || len(library.Locations) != 1 || library.Locations[0] != desired.path {
				return fmt.Errorf("Jellyfin %s must use only %s", desired.name, desired.path)
			}
			found = true
		}
		if !found {
			query := url.Values{"name": {desired.name}, "collectionType": {desired.collectionType}, "paths": {desired.path}, "refreshLibrary": {"true"}}
			if err := client.doJSON(ctx, http.MethodPost, "/Library/VirtualFolders?"+query.Encode(), nil, auth.AccessToken, nil); err != nil {
				return fmt.Errorf("create Jellyfin %s: %w", desired.name, err)
			}
		}
	}
	users, err := client.users(ctx, auth.AccessToken)
	if err != nil {
		return err
	}
	for _, currentUser := range users {
		if deletionDisabled(currentUser.Policy) {
			continue
		}
		currentUser.Policy["EnableContentDeletion"] = false
		currentUser.Policy["EnableContentDeletionFromFolders"] = []any{}
		if err := client.doJSON(ctx, http.MethodPost, "/Users/"+url.PathEscape(currentUser.ID)+"/Policy", currentUser.Policy, auth.AccessToken, nil); err != nil {
			return fmt.Errorf("disable destructive Jellyfin deletion for user %s: %w", currentUser.ID, err)
		}
	}
	return nil
}

func (client *Client) DestructiveDeletionDisabled(ctx context.Context, credentials Credentials) (bool, error) {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return false, err
	}
	users, err := client.users(ctx, auth.AccessToken)
	if err != nil {
		return false, err
	}
	for _, currentUser := range users {
		if !deletionDisabled(currentUser.Policy) {
			return false, nil
		}
	}
	return len(users) != 0, nil
}

func (client *Client) users(ctx context.Context, token string) ([]user, error) {
	var users []user
	if err := client.doJSON(ctx, http.MethodGet, "/Users", nil, token, &users); err != nil {
		return nil, fmt.Errorf("observe Jellyfin user policies: %w", err)
	}
	return users, nil
}

func deletionDisabled(policy map[string]any) bool {
	deletionEnabled, _ := policy["EnableContentDeletion"].(bool)
	folders, _ := policy["EnableContentDeletionFromFolders"].([]any)
	return !deletionEnabled && len(folders) == 0
}

func (client *Client) MoviePlaybackReady(ctx context.Context, credentials Credentials, moviePath string) (bool, error) {
	return client.playbackReady(ctx, credentials, "Movie", moviePath)
}

func (client *Client) EpisodePlaybackReady(ctx context.Context, credentials Credentials, episodePath string) (bool, error) {
	return client.playbackReady(ctx, credentials, "Episode", episodePath)
}

func (client *Client) playbackReady(ctx context.Context, credentials Credentials, itemType, mediaPath string) (bool, error) {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return false, err
	}
	query := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {itemType}, "Fields": {"Path"}}
	var items struct {
		Items []struct {
			ID   string `json:"Id"`
			Path string `json:"Path"`
		} `json:"Items"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/Users/"+url.PathEscape(auth.User.ID)+"/Items?"+query.Encode(), nil, auth.AccessToken, &items); err != nil {
		return false, fmt.Errorf("discover imported %s through Jellyfin: %w", strings.ToLower(itemType), err)
	}
	for _, item := range items.Items {
		if item.Path != mediaPath {
			continue
		}
		var playback struct {
			MediaSources []struct {
				Path               string `json:"Path"`
				SupportsDirectPlay bool   `json:"SupportsDirectPlay"`
			} `json:"MediaSources"`
		}
		path := "/Items/" + url.PathEscape(item.ID) + "/PlaybackInfo?UserId=" + url.QueryEscape(auth.User.ID)
		if err := client.doJSON(ctx, http.MethodGet, path, nil, auth.AccessToken, &playback); err != nil {
			return false, fmt.Errorf("inspect Jellyfin playback readiness: %w", err)
		}
		for _, source := range playback.MediaSources {
			if source.Path == mediaPath && source.SupportsDirectPlay {
				return true, nil
			}
		}
	}
	return false, nil
}

func (client *Client) authenticate(ctx context.Context, credentials Credentials) (authentication, error) {
	var result authentication
	if err := client.doJSON(ctx, http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": credentials.Username, "Pw": credentials.Password}, "", &result); err != nil {
		return result, fmt.Errorf("authenticate Jellyfin user: %w", err)
	}
	if result.AccessToken == "" || result.User.ID == "" {
		return result, fmt.Errorf("authenticate Jellyfin user: response omitted token or user identity")
	}
	return result, nil
}

func (client *Client) doJSON(ctx context.Context, method, path string, input any, token string, output any) error {
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
	request.Header.Set("Authorization", `MediaBrowser Client="media-stack", Device="media-stack", DeviceId="media-stack-cli", Version="1"`)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("X-Emby-Token", token)
	}
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
