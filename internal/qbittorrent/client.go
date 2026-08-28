package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var ErrAuthenticationRejected = errors.New("qBittorrent rejected Web UI credentials")

var (
	ErrUnsupportedLoginResponse = errors.New("qBittorrent returned an unsupported login response")
	ErrProtectedObservation     = errors.New("qBittorrent protected API observation failed")
)

type Credentials struct {
	Username string
	Password string
}

type DeclaredConfiguration struct {
	Credentials Credentials
	Port        int
}

const (
	defaultSavePath  = "/data/torrents"
	sevenDaysMinutes = 7 * 24 * 60
)

type Client struct {
	baseURL string
	http    *http.Client
	cookie  *http.Cookie
}

type preferences struct {
	SavePath                 string  `json:"save_path"`
	AutomaticManagement      bool    `json:"auto_tmm_enabled"`
	RelocateOnTorrentChange  bool    `json:"torrent_changed_tmm_enabled"`
	RelocateOnSavePathChange bool    `json:"save_path_changed_tmm_enabled"`
	RelocateOnCategoryChange bool    `json:"category_changed_tmm_enabled"`
	RatioLimitEnabled        bool    `json:"max_ratio_enabled"`
	RatioLimit               float64 `json:"max_ratio"`
	SeedingTimeLimitEnabled  bool    `json:"max_seeding_time_enabled"`
	SeedingTimeLimitMinutes  int     `json:"max_seeding_time"`
	LimitReachedAction       int     `json:"max_ratio_act"`
}

type Category struct {
	Name     string `json:"name"`
	SavePath string `json:"savePath"`
}

func New(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: client}
}

func (client *Client) Login(ctx context.Context, username, password string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/v2/auth/login", strings.NewReader(url.Values{
		"username": {username}, "password": {password},
	}.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", client.baseURL)
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("authenticate to qBittorrent API: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	cookies := response.Cookies()
	if len(cookies) > 0 {
		client.cookie = cookies[0]
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: HTTP %d", ErrAuthenticationRejected, response.StatusCode)
	}
	if response.StatusCode == http.StatusNoContent {
		if err := client.ObserveProtectedAPI(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrProtectedObservation, err)
		}
		return nil
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("%w: HTTP %d: %s", ErrUnsupportedLoginResponse, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(cookies) == 0 {
		return fmt.Errorf("%w: legacy response did not set a session cookie", ErrUnsupportedLoginResponse)
	}
	client.cookie = cookies[0]
	return nil
}

func (client *Client) ObserveProtectedAPI(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/v2/app/version", nil)
	if err != nil {
		return err
	}
	client.authorize(request)
	request.Header.Set("Referer", client.baseURL)
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("observe protected qBittorrent API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("observe protected qBittorrent API: HTTP %d", response.StatusCode)
	}
	return nil
}

func (client *Client) ReconcileWebUICredentials(ctx context.Context, credentials Credentials) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fmt.Errorf("qBittorrent Web UI username and password are required")
	}
	encoded, err := json.Marshal(map[string]string{
		"web_ui_username": credentials.Username,
		"web_ui_password": credentials.Password,
	})
	if err != nil {
		return err
	}
	return client.postForm(ctx, "/api/v2/app/setPreferences", url.Values{"json": {string(encoded)}})
}

func (client *Client) ReconcileAcquisitionPolicy(ctx context.Context) (bool, error) {
	var observed preferences
	if err := client.getJSON(ctx, "/api/v2/app/preferences", &observed); err != nil {
		return false, err
	}
	declared := preferences{
		SavePath: defaultSavePath, AutomaticManagement: true,
		RelocateOnTorrentChange: true, RelocateOnSavePathChange: true, RelocateOnCategoryChange: true,
		RatioLimitEnabled: true, RatioLimit: 1,
		SeedingTimeLimitEnabled: true, SeedingTimeLimitMinutes: sevenDaysMinutes,
		LimitReachedAction: 0,
	}
	changed := false
	if observed != declared {
		encoded, err := json.Marshal(declared)
		if err != nil {
			return false, err
		}
		if err := client.postForm(ctx, "/api/v2/app/setPreferences", url.Values{"json": {string(encoded)}}); err != nil {
			return false, err
		}
		changed = true
	}

	var categories map[string]Category
	if err := client.getJSON(ctx, "/api/v2/torrents/categories", &categories); err != nil {
		return false, err
	}
	for _, category := range []Category{{Name: "movies", SavePath: "movies"}, {Name: "series", SavePath: "series"}} {
		current, exists := categories[category.Name]
		if exists && current.SavePath == category.SavePath {
			continue
		}
		endpoint := "/api/v2/torrents/createCategory"
		if exists {
			endpoint = "/api/v2/torrents/editCategory"
		}
		if err := client.postForm(ctx, endpoint, url.Values{"category": {category.Name}, "savePath": {category.SavePath}}); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func (client *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+endpoint, nil)
	if err != nil {
		return err
	}
	client.authorize(request)
	request.Header.Set("Referer", client.baseURL)
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("observe qBittorrent API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("observe qBittorrent API %s: HTTP %d: %s", endpoint, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode qBittorrent API %s: %w", endpoint, err)
	}
	return nil
}

func (client *Client) postForm(ctx context.Context, endpoint string, values url.Values) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client.authorize(request)
	request.Header.Set("Referer", client.baseURL)
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("reconcile qBittorrent API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("reconcile qBittorrent API %s: HTTP %d: %s", endpoint, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (client *Client) authorize(request *http.Request) {
	if client.cookie != nil {
		request.AddCookie(client.cookie)
	}
}

type Torrent struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Progress float64 `json:"progress"`
	SavePath string  `json:"save_path"`
}

type TorrentFile struct {
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
}

func (client *Client) CompletedMovie(ctx context.Context, releaseTitle string) (Torrent, []TorrentFile, bool, error) {
	return client.completedInCategory(ctx, "movies", releaseTitle)
}

func (client *Client) CompletedSeries(ctx context.Context, releaseTitle string) (Torrent, []TorrentFile, bool, error) {
	return client.completedInCategory(ctx, "series", releaseTitle)
}

func (client *Client) completedInCategory(ctx context.Context, category, releaseTitle string) (Torrent, []TorrentFile, bool, error) {
	var torrents []Torrent
	if err := client.getJSON(ctx, "/api/v2/torrents/info?category="+url.QueryEscape(category), &torrents); err != nil {
		return Torrent{}, nil, false, err
	}
	for _, torrent := range torrents {
		if !strings.EqualFold(torrent.Name, releaseTitle) || torrent.Category != category || torrent.Progress < 1 {
			continue
		}
		var files []TorrentFile
		if err := client.getJSON(ctx, "/api/v2/torrents/files?hash="+url.QueryEscape(torrent.Hash), &files); err != nil {
			return Torrent{}, nil, false, err
		}
		return torrent, files, true, nil
	}
	return Torrent{}, nil, false, nil
}
