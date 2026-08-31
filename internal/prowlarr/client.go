package prowlarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

const (
	internetArchiveDefinition = "internetarchive"
	internalProwlarrURL       = "http://prowlarr:9696"
	internalRadarrURL         = "http://radarr:7878"
	internalSonarrURL         = "http://sonarr:8989"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type field struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type indexer struct {
	ID                 int     `json:"id,omitempty"`
	Enable             bool    `json:"enable"`
	Redirect           bool    `json:"redirect"`
	SupportsRSS        bool    `json:"supportsRss"`
	SupportsSearch     bool    `json:"supportsSearch"`
	AppProfileID       int     `json:"appProfileId"`
	Protocol           string  `json:"protocol"`
	Privacy            string  `json:"privacy"`
	Priority           int     `json:"priority"`
	Name               string  `json:"name"`
	DefinitionName     string  `json:"definitionName"`
	ImplementationName string  `json:"implementationName"`
	Implementation     string  `json:"implementation"`
	ConfigContract     string  `json:"configContract"`
	Fields             []field `json:"fields"`
	Tags               []int   `json:"tags"`
}

type application struct {
	ID                 int     `json:"id,omitempty"`
	Enable             bool    `json:"enable"`
	Name               string  `json:"name"`
	SyncLevel          string  `json:"syncLevel"`
	ImplementationName string  `json:"implementationName"`
	Implementation     string  `json:"implementation"`
	ConfigContract     string  `json:"configContract"`
	Fields             []field `json:"fields"`
	Tags               []int   `json:"tags"`
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
	if err := client.getJSON(ctx, "/api/v1/system/status", &status); err != nil {
		return err
	}
	if status.AppName != "Prowlarr" {
		return fmt.Errorf("Prowlarr API reported application %q", status.AppName)
	}
	return nil
}

func (client *Client) ReconcileLibraryDiscovery(ctx context.Context, radarrAPIKey, sonarrAPIKey string) (bool, error) {
	var indexers []indexer
	if err := client.getJSON(ctx, "/api/v1/indexer", &indexers); err != nil {
		return false, err
	}
	changed := false
	desiredIndexer := declaredInternetArchive()
	foundIndexer := false
	for _, current := range indexers {
		if current.DefinitionName == internetArchiveDefinition && indexerMatches(current, desiredIndexer) {
			foundIndexer = true
			break
		}
	}
	if !foundIndexer {
		if err := client.sendJSON(ctx, http.MethodPost, "/api/v1/indexer", desiredIndexer); err != nil {
			return false, err
		}
		changed = true
	}

	var applications []application
	if err := client.getJSON(ctx, "/api/v1/applications", &applications); err != nil {
		return false, err
	}
	desiredApplications := []application{declaredRadarrApplication(radarrAPIKey), declaredSonarrApplication(sonarrAPIKey)}
	for _, desiredApplication := range desiredApplications {
		foundApplication := false
		for _, current := range applications {
			if current.Implementation == desiredApplication.Implementation && applicationMatches(current, desiredApplication) {
				foundApplication = true
				break
			}
		}
		if foundApplication {
			continue
		}
		if err := client.sendJSON(ctx, http.MethodPost, "/api/v1/applications", desiredApplication); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func declaredInternetArchive() indexer {
	return indexer{
		Enable: true, Redirect: false, SupportsRSS: true, SupportsSearch: true, AppProfileID: 1,
		Protocol: "torrent", Privacy: "public", Priority: 25, Name: "Internet Archive",
		DefinitionName: internetArchiveDefinition, ImplementationName: "Cardigann",
		Implementation: "Cardigann", ConfigContract: "CardigannSettings", Tags: []int{},
		Fields: []field{
			{Name: "definitionFile", Value: internetArchiveDefinition},
			{Name: "baseUrl", Value: "https://archive.org/"},
			{Name: "titleOnly", Value: true},
			{Name: "noMagnet", Value: false},
			{Name: "sort", Value: 2},
			{Name: "type", Value: 1},
		},
	}
}

func declaredRadarrApplication(apiKey string) application {
	standardMovieCategories := []int{2000, 2010, 2020, 2030, 2040, 2045, 2050, 2060, 2070, 2080, 2090}
	return application{
		Enable: true, Name: "Radarr", SyncLevel: "fullSync",
		ImplementationName: "Radarr", Implementation: "Radarr", ConfigContract: "RadarrSettings", Tags: []int{},
		Fields: []field{
			{Name: "prowlarrUrl", Value: internalProwlarrURL},
			{Name: "baseUrl", Value: internalRadarrURL},
			{Name: "apiKey", Value: apiKey},
			{Name: "syncCategories", Value: standardMovieCategories},
		},
	}
}

func declaredSonarrApplication(apiKey string) application {
	return application{
		Enable: true, Name: "Sonarr", SyncLevel: "fullSync",
		ImplementationName: "Sonarr", Implementation: "Sonarr", ConfigContract: "SonarrSettings", Tags: []int{},
		Fields: []field{
			{Name: "prowlarrUrl", Value: internalProwlarrURL},
			{Name: "baseUrl", Value: internalSonarrURL},
			{Name: "apiKey", Value: apiKey},
			{Name: "syncCategories", Value: []int{5000, 5010, 5020, 5030, 5040, 5045, 5050, 5090}},
			{Name: "animeSyncCategories", Value: []int{5070}},
			{Name: "syncAnimeStandardFormatSearch", Value: true},
			{Name: "syncRejectBlocklistedTorrentHashesWhileGrabbing", Value: false},
		},
	}
}

func indexerMatches(observed, desired indexer) bool {
	return observed.Enable == desired.Enable && observed.Redirect == desired.Redirect &&
		observed.SupportsRSS == desired.SupportsRSS && observed.SupportsSearch == desired.SupportsSearch &&
		observed.AppProfileID == desired.AppProfileID &&
		observed.Protocol == desired.Protocol && observed.Privacy == desired.Privacy &&
		observed.Priority == desired.Priority && observed.Name == desired.Name &&
		observed.Implementation == desired.Implementation && observed.ConfigContract == desired.ConfigContract &&
		fieldsMatch(observed.Fields, desired.Fields)
}

func applicationMatches(observed, desired application) bool {
	return observed.Enable == desired.Enable && observed.Name == desired.Name && observed.SyncLevel == desired.SyncLevel &&
		observed.Implementation == desired.Implementation && observed.ConfigContract == desired.ConfigContract &&
		fieldsMatch(observed.Fields, desired.Fields)
}

func fieldsMatch(observed, desired []field) bool {
	values := make(map[string]any, len(observed))
	for _, item := range observed {
		values[item.Name] = item.Value
	}
	for _, item := range desired {
		if item.Name == "apiKey" {
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
	if items, ok := value.([]any); ok {
		normalized := make([]int, len(items))
		for index, item := range items {
			number, ok := item.(float64)
			if !ok || number != float64(int(number)) {
				return value
			}
			normalized[index] = int(number)
		}
		return normalized
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
		return fmt.Errorf("observe Prowlarr API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError("observe", endpoint, response)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode Prowlarr API %s: %w", endpoint, err)
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
		return fmt.Errorf("reconcile Prowlarr API %s: %w", endpoint, err)
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
	return fmt.Errorf("%s Prowlarr API %s: HTTP %d: %s", action, endpoint, response.StatusCode, strings.TrimSpace(string(body)))
}
