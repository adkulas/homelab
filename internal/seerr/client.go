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

type destinationTestResponse struct {
	Profiles []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"profiles"`
	RootFolders []struct {
		Path string `json:"path"`
	} `json:"rootFolders"`
}

type sonarrDestination struct {
	ID                           int    `json:"id,omitempty"`
	Name                         string `json:"name"`
	Hostname                     string `json:"hostname"`
	Port                         int    `json:"port"`
	APIKey                       string `json:"apiKey"`
	UseSSL                       bool   `json:"useSsl"`
	BaseURL                      string `json:"baseUrl"`
	ActiveProfileID              int    `json:"activeProfileId"`
	ActiveLanguageProfileID      int    `json:"activeLanguageProfileId,omitempty"`
	ActiveProfileName            string `json:"activeProfileName"`
	ActiveDirectory              string `json:"activeDirectory"`
	SeriesType                   string `json:"seriesType"`
	AnimeSeriesType              string `json:"animeSeriesType,omitempty"`
	ActiveAnimeProfileID         int    `json:"activeAnimeProfileId,omitempty"`
	ActiveAnimeLanguageProfileID int    `json:"activeAnimeLanguageProfileId,omitempty"`
	ActiveAnimeProfileName       string `json:"activeAnimeProfileName,omitempty"`
	ActiveAnimeDirectory         string `json:"activeAnimeDirectory,omitempty"`
	Tags                         []int  `json:"tags"`
	AnimeTags                    []int  `json:"animeTags"`
	Is4K                         bool   `json:"is4k"`
	IsDefault                    bool   `json:"isDefault"`
	EnableSeasonFolders          bool   `json:"enableSeasonFolders"`
	ExternalURL                  string `json:"externalUrl"`
	SyncEnabled                  bool   `json:"syncEnabled"`
	PreventSearch                bool   `json:"preventSearch"`
	TagRequests                  bool   `json:"tagRequests"`
	MonitorNewItems              string `json:"monitorNewItems"`
}

func internalSonarrDestination(apiKey string) sonarrDestination {
	return sonarrDestination{Hostname: "sonarr", Port: 8989, APIKey: apiKey, UseSSL: false, BaseURL: ""}
}

func (destination sonarrDestination) hasSameEndpoint(other sonarrDestination) bool {
	return destination.Hostname == other.Hostname &&
		destination.Port == other.Port &&
		destination.UseSSL == other.UseSSL &&
		destination.BaseURL == other.BaseURL
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

func reconcileDestination[T any](
	ctx context.Context,
	client *Client,
	serviceName, settingsPath, profileName, rootPath, rootName string,
	connection T,
	desiredForProfile func(int) T,
	hasSameEndpoint func(T, T) bool,
	identity func(T) int,
	withIdentity func(T, int) T,
) error {
	var tested destinationTestResponse
	if err := client.doJSON(ctx, http.MethodPost, settingsPath+"/test", connection, &tested); err != nil {
		return fmt.Errorf("test Seerr %s destination: %w", serviceName, err)
	}
	profileID := 0
	profileMatches := 0
	for _, profile := range tested.Profiles {
		if profile.Name == profileName {
			profileMatches++
			profileID = profile.ID
		}
	}
	if profileMatches == 0 || profileID <= 0 {
		return fmt.Errorf("reconcile Seerr %s destination: %s profile %q is missing", serviceName, serviceName, profileName)
	}
	if profileMatches > 1 {
		return fmt.Errorf("reconcile Seerr %s destination: %s returned duplicate %q profiles", serviceName, serviceName, profileName)
	}
	rootFound := false
	for _, root := range tested.RootFolders {
		if root.Path == rootPath {
			rootFound = true
			break
		}
	}
	if !rootFound {
		return fmt.Errorf("reconcile Seerr %s destination: %s root is missing", serviceName, rootName)
	}
	desired := desiredForProfile(profileID)
	var destinations []T
	if err := client.doJSON(ctx, http.MethodGet, settingsPath, nil, &destinations); err != nil {
		return fmt.Errorf("observe Seerr %s destination: %w", serviceName, err)
	}
	match := -1
	for index, destination := range destinations {
		if hasSameEndpoint(destination, desired) {
			if match != -1 {
				return fmt.Errorf("reconcile Seerr %s destination: multiple internal %s destinations exist", serviceName, serviceName)
			}
			match = index
		}
	}
	if match == -1 {
		if err := client.doJSON(ctx, http.MethodPost, settingsPath, desired, nil); err != nil {
			return fmt.Errorf("create Seerr %s destination: %w", serviceName, err)
		}
		return nil
	}
	desired = withIdentity(desired, identity(destinations[match]))
	if reflect.DeepEqual(destinations[match], desired) {
		return nil
	}
	path := fmt.Sprintf("%s/%d", settingsPath, identity(desired))
	if err := client.doJSON(ctx, http.MethodPut, path, desired, nil); err != nil {
		return fmt.Errorf("repair Seerr %s destination: %w", serviceName, err)
	}
	return nil
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
	payload := map[string]any{"mediaType": "movie", "mediaId": tmdbID, "is4k": false}
	if err := client.requestApproved(ctx, credentials, payload); err != nil {
		return fmt.Errorf("request movie through Seerr: %w", err)
	}
	return nil
}

func (client *Client) RequestSeries(ctx context.Context, credentials Credentials, tmdbID, seasonNumber int) error {
	if tmdbID <= 0 {
		return fmt.Errorf("request series through Seerr: TMDB identity must be positive")
	}
	if seasonNumber < 0 {
		return fmt.Errorf("request series through Seerr: season number must be non-negative")
	}
	payload := map[string]any{"mediaType": "tv", "mediaId": tmdbID, "seasons": []int{seasonNumber}, "is4k": false}
	if err := client.requestApproved(ctx, credentials, payload); err != nil {
		return fmt.Errorf("request series through Seerr: %w", err)
	}
	return nil
}

func (client *Client) requestApproved(ctx context.Context, credentials Credentials, payload map[string]any) error {
	if _, err := client.authenticateJellyfin(ctx, credentials, false); err != nil {
		return err
	}
	var created struct {
		ID     int `json:"id"`
		Status int `json:"status"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/request", payload, &created); err != nil {
		return err
	}
	if created.ID == 0 || created.Status != 2 {
		return fmt.Errorf("response did not contain an approved request")
	}
	return nil
}

func (client *Client) ReconcileSonarr(ctx context.Context, apiKey, profileName string) error {
	connection := internalSonarrDestination(apiKey)
	return reconcileDestination(
		ctx, client, "Sonarr", "/api/v1/settings/sonarr", profileName, "/data/media/series", "Series Library",
		connection,
		func(profileID int) sonarrDestination {
			desired := connection
			desired.Name = "Sonarr"
			desired.ActiveProfileID, desired.ActiveProfileName, desired.ActiveDirectory = profileID, profileName, "/data/media/series"
			desired.SeriesType = "standard"
			desired.Tags, desired.AnimeTags = []int{}, []int{}
			desired.Is4K, desired.IsDefault, desired.EnableSeasonFolders = false, true, true
			desired.ExternalURL, desired.SyncEnabled, desired.PreventSearch = "", true, false
			desired.TagRequests, desired.MonitorNewItems = false, "all"
			return desired
		},
		sonarrDestination.hasSameEndpoint,
		func(destination sonarrDestination) int { return destination.ID },
		func(destination sonarrDestination, id int) sonarrDestination {
			destination.ID = id
			return destination
		},
	)
}

func (client *Client) ReconcileRadarr(ctx context.Context, apiKey, profileName string) error {
	connection := internalRadarrDestination(apiKey)
	return reconcileDestination(
		ctx, client, "Radarr", "/api/v1/settings/radarr", profileName, "/data/media/movies", "Movie Library",
		connection,
		func(profileID int) radarrDestination {
			desired := connection
			desired.Name = "Radarr"
			desired.ActiveProfileID, desired.ActiveProfileName, desired.ActiveDirectory = profileID, profileName, "/data/media/movies"
			desired.Tags, desired.Is4K, desired.IsDefault = []int{}, false, true
			desired.MinimumAvailability, desired.ExternalURL, desired.SyncEnabled = "released", "", true
			desired.PreventSearch, desired.TagRequests, desired.OverrideRule = false, false, []int{}
			return desired
		},
		radarrDestination.hasSameEndpoint,
		func(destination radarrDestination) int { return destination.ID },
		func(destination radarrDestination, id int) radarrDestination {
			destination.ID = id
			return destination
		},
	)
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
