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

const movieLibraryPath = "/data/media/movies"

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

func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

func (client *Client) ReconcileMovieLibrary(ctx context.Context, credentials Credentials) error {
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
			path string
			body any
		}{
			{"/Startup/Configuration", map[string]any{"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en"}},
			{"/Startup/User", map[string]string{"Name": credentials.Username, "Password": credentials.Password}},
			{"/Startup/RemoteAccess", map[string]bool{"EnableRemoteAccess": false, "EnableAutomaticPortMapping": false}},
			{"/Startup/Complete", map[string]any{}},
		}
		for _, step := range steps {
			if err := client.doJSON(ctx, http.MethodPost, step.path, step.body, "", nil); err != nil {
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
	found := false
	for _, library := range libraries {
		if library.CollectionType != "movies" && library.Name != "Movie Library" {
			continue
		}
		if library.Name != "Movie Library" || len(library.Locations) != 1 || library.Locations[0] != movieLibraryPath {
			return fmt.Errorf("Jellyfin Movie Library must use only %s", movieLibraryPath)
		}
		found = true
	}
	if !found {
		query := url.Values{"name": {"Movie Library"}, "collectionType": {"movies"}, "paths": {movieLibraryPath}, "refreshLibrary": {"true"}}
		if err := client.doJSON(ctx, http.MethodPost, "/Library/VirtualFolders?"+query.Encode(), nil, auth.AccessToken, nil); err != nil {
			return fmt.Errorf("create Jellyfin Movie Library: %w", err)
		}
	}
	deletionEnabled, _ := auth.User.Policy["EnableContentDeletion"].(bool)
	folderDeletion, _ := auth.User.Policy["EnableContentDeletionFromFolders"].([]any)
	if deletionEnabled || len(folderDeletion) != 0 {
		auth.User.Policy["EnableContentDeletion"] = false
		auth.User.Policy["EnableContentDeletionFromFolders"] = []any{}
		if err := client.doJSON(ctx, http.MethodPost, "/Users/"+url.PathEscape(auth.User.ID)+"/Policy", auth.User.Policy, auth.AccessToken, nil); err != nil {
			return fmt.Errorf("disable destructive Jellyfin deletion: %w", err)
		}
	}
	return nil
}

func (client *Client) MoviePlaybackReady(ctx context.Context, credentials Credentials, moviePath string) (bool, error) {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return false, err
	}
	query := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Movie"}, "Fields": {"Path"}}
	var items struct {
		Items []struct {
			ID   string `json:"Id"`
			Path string `json:"Path"`
		} `json:"Items"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/Users/"+url.PathEscape(auth.User.ID)+"/Items?"+query.Encode(), nil, auth.AccessToken, &items); err != nil {
		return false, fmt.Errorf("discover imported movie through Jellyfin: %w", err)
	}
	for _, item := range items.Items {
		if item.Path != moviePath {
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
			if source.Path == moviePath && source.SupportsDirectPlay {
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
