package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/adkulas/homelab/internal/qbittorrent"
)

const seriesLibraryRoot = "/data/media/series"

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type rootFolder struct {
	Path string `json:"path"`
}

type field struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type downloadClient struct {
	Enable                   bool    `json:"enable"`
	Protocol                 string  `json:"protocol"`
	Priority                 int     `json:"priority"`
	RemoveCompletedDownloads bool    `json:"removeCompletedDownloads"`
	RemoveFailedDownloads    bool    `json:"removeFailedDownloads"`
	Name                     string  `json:"name"`
	Fields                   []field `json:"fields"`
	ImplementationName       string  `json:"implementationName"`
	Implementation           string  `json:"implementation"`
	ConfigContract           string  `json:"configContract"`
	Tags                     []int   `json:"tags"`
}

func New(baseURL, apiKey string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: client}
}

func (client *Client) Ready(ctx context.Context) error {
	var status struct {
		AppName string `json:"appName"`
	}
	if err := client.getJSON(ctx, "/api/v3/system/status", &status); err != nil {
		return err
	}
	if status.AppName != "Sonarr" {
		return fmt.Errorf("Sonarr API reported application %q", status.AppName)
	}
	return nil
}

func (client *Client) ReconcileSeriesLibrary(ctx context.Context, qbittorrentConfiguration qbittorrent.DeclaredConfiguration) (bool, error) {
	var roots []rootFolder
	if err := client.getJSON(ctx, "/api/v3/rootfolder", &roots); err != nil {
		return false, err
	}
	changed := false
	foundRoot := false
	for _, root := range roots {
		foundRoot = foundRoot || root.Path == seriesLibraryRoot
	}
	if !foundRoot {
		if err := client.sendJSON(ctx, http.MethodPost, "/api/v3/rootfolder", rootFolder{Path: seriesLibraryRoot}); err != nil {
			return false, err
		}
		changed = true
	}

	var observed []downloadClient
	if err := client.getJSON(ctx, "/api/v3/downloadclient", &observed); err != nil {
		return false, err
	}
	desired := declaredDownloadClient(qbittorrentConfiguration)
	for _, current := range observed {
		if current.Implementation == desired.Implementation && downloadClientMatches(current, desired) {
			return changed, nil
		}
	}
	if err := client.sendJSON(ctx, http.MethodPost, "/api/v3/downloadclient", desired); err != nil {
		return false, err
	}
	return true, nil
}

func declaredDownloadClient(configuration qbittorrent.DeclaredConfiguration) downloadClient {
	return downloadClient{
		Enable: true, Protocol: "torrent", Priority: 1,
		RemoveCompletedDownloads: true, RemoveFailedDownloads: true,
		Name: "qBittorrent", ImplementationName: "qBittorrent",
		Implementation: "QBittorrent", ConfigContract: "QBittorrentSettings", Tags: []int{},
		Fields: []field{
			{Name: "host", Value: "qbittorrent"}, {Name: "port", Value: configuration.Port},
			{Name: "useSsl", Value: false}, {Name: "urlBase", Value: ""},
			{Name: "username", Value: configuration.Credentials.Username}, {Name: "password", Value: configuration.Credentials.Password},
			{Name: "tvCategory", Value: "series"}, {Name: "tvImportedCategory", Value: ""},
			{Name: "recentTvPriority", Value: 0}, {Name: "olderTvPriority", Value: 0},
			{Name: "initialState", Value: 0}, {Name: "sequentialOrder", Value: false},
			{Name: "firstAndLast", Value: false},
		},
	}
}

func downloadClientMatches(observed, desired downloadClient) bool {
	if observed.Enable != desired.Enable || observed.Protocol != desired.Protocol || observed.Priority != desired.Priority ||
		observed.RemoveCompletedDownloads != desired.RemoveCompletedDownloads || observed.RemoveFailedDownloads != desired.RemoveFailedDownloads ||
		observed.Name != desired.Name || observed.ConfigContract != desired.ConfigContract {
		return false
	}
	values := make(map[string]any, len(observed.Fields))
	for _, item := range observed.Fields {
		values[item.Name] = item.Value
	}
	for _, item := range desired.Fields {
		if item.Name == "password" {
			continue
		}
		if !reflect.DeepEqual(normalizeJSONValue(values[item.Name]), normalizeJSONValue(item.Value)) {
			return false
		}
	}
	return true
}

func normalizeJSONValue(value any) any {
	if number, ok := value.(float64); ok && number == float64(int(number)) {
		return int(number)
	}
	return value
}

func (client *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.do(request)
	if err != nil {
		return fmt.Errorf("observe Sonarr API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError("observe", endpoint, response)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode Sonarr API %s: %w", endpoint, err)
	}
	return nil
}

func (client *Client) sendJSON(ctx context.Context, method, endpoint string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.do(request)
	if err != nil {
		return fmt.Errorf("reconcile Sonarr API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return responseError("reconcile", endpoint, response)
	}
	return nil
}

func (client *Client) do(request *http.Request) (*http.Response, error) {
	request.Header.Set("X-Api-Key", client.apiKey)
	return client.http.Do(request)
}

func responseError(action, endpoint string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("%s Sonarr API %s: HTTP %d: %s", action, endpoint, response.StatusCode, strings.TrimSpace(string(body)))
}

type EpisodeFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (client *Client) AcquireLegalEpisode(ctx context.Context, tvdbID, seasonNumber, episodeNumber int, releaseTitle, indexer string) (int, int, error) {
	var seriesItems []struct {
		ID int `json:"id"`
	}
	if err := client.getJSON(ctx, fmt.Sprintf("/api/v3/series?tvdbId=%d", tvdbID), &seriesItems); err != nil {
		return 0, 0, err
	}
	seriesID := 0
	if len(seriesItems) > 0 {
		seriesID = seriesItems[0].ID
	} else {
		var lookup []map[string]any
		if err := client.getJSON(ctx, fmt.Sprintf("/api/v3/series/lookup?term=tvdb:%d", tvdbID), &lookup); err != nil {
			return 0, 0, err
		}
		if len(lookup) == 0 {
			return 0, 0, fmt.Errorf("Sonarr did not find TVDB series %d", tvdbID)
		}
		var profiles []struct {
			ID int `json:"id"`
		}
		if err := client.getJSON(ctx, "/api/v3/qualityprofile", &profiles); err != nil {
			return 0, 0, err
		}
		if len(profiles) == 0 {
			return 0, 0, fmt.Errorf("Sonarr has no quality profile for legal verification")
		}
		series := lookup[0]
		series["qualityProfileId"] = profiles[0].ID
		series["rootFolderPath"] = seriesLibraryRoot
		series["monitored"] = true
		series["seasonFolder"] = true
		series["seriesType"] = "standard"
		series["addOptions"] = map[string]any{"searchForMissingEpisodes": false}
		var created struct {
			ID int `json:"id"`
		}
		if err := client.sendJSONResult(ctx, http.MethodPost, "/api/v3/series", series, &created); err != nil {
			return 0, 0, err
		}
		seriesID = created.ID
	}
	if seriesID == 0 {
		return 0, 0, fmt.Errorf("Sonarr did not provide a series identity for TVDB series %d", tvdbID)
	}

	var episodes []struct {
		ID            int `json:"id"`
		SeasonNumber  int `json:"seasonNumber"`
		EpisodeNumber int `json:"episodeNumber"`
	}
	if err := client.getJSON(ctx, fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID), &episodes); err != nil {
		return 0, 0, err
	}
	episodeID := 0
	for _, episode := range episodes {
		if episode.SeasonNumber == seasonNumber && episode.EpisodeNumber == episodeNumber {
			episodeID = episode.ID
			break
		}
	}
	if episodeID == 0 {
		return 0, 0, fmt.Errorf("Sonarr did not find season %d episode %d for TVDB series %d", seasonNumber, episodeNumber, tvdbID)
	}

	var releases []json.RawMessage
	if err := client.getJSON(ctx, fmt.Sprintf("/api/v3/release?episodeId=%d", episodeID), &releases); err != nil {
		return 0, 0, err
	}
	for _, raw := range releases {
		var release struct {
			Title   string `json:"title"`
			Indexer string `json:"indexer"`
		}
		if err := json.Unmarshal(raw, &release); err != nil {
			return 0, 0, fmt.Errorf("decode Sonarr release: %w", err)
		}
		if release.Title == releaseTitle && release.Indexer == indexer {
			if err := client.sendJSON(ctx, http.MethodPost, "/api/v3/release", raw); err != nil {
				return 0, 0, err
			}
			return seriesID, episodeID, nil
		}
	}
	return 0, 0, fmt.Errorf("Sonarr did not discover release %q from %s", releaseTitle, indexer)
}

func (client *Client) ImportedEpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	var files []EpisodeFile
	if err := client.getJSON(ctx, fmt.Sprintf("/api/v3/episodefile?seriesId=%d", seriesID), &files); err != nil {
		return nil, err
	}
	return files, nil
}

func (client *Client) sendJSONResult(ctx context.Context, method, endpoint string, body, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.do(request)
	if err != nil {
		return fmt.Errorf("reconcile Sonarr API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return responseError("reconcile", endpoint, response)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode Sonarr API %s: %w", endpoint, err)
	}
	return nil
}
