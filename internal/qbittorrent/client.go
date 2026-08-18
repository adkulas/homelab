package qbittorrent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("authenticate to qBittorrent API: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	cookies := response.Cookies()
	if len(cookies) == 0 {
		return fmt.Errorf("authenticate to qBittorrent API: response did not set a session cookie")
	}
	client.cookie = cookies[0]
	return nil
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
	var torrents []Torrent
	if err := client.getJSON(ctx, "/api/v2/torrents/info?category=movies", &torrents); err != nil {
		return Torrent{}, nil, false, err
	}
	for _, torrent := range torrents {
		if !strings.EqualFold(torrent.Name, releaseTitle) || torrent.Category != "movies" || torrent.Progress < 1 {
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
