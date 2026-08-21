package seerr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

const requestPermission = 32

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

type jellyfinSettings struct {
	IP                string `json:"ip"`
	Port              int    `json:"port"`
	UseSSL            bool   `json:"useSsl"`
	URLBase           string `json:"urlBase"`
	ExternalHostname  string `json:"externalHostname"`
	ForgotPasswordURL string `json:"jellyfinForgotPasswordUrl"`
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

func (client *Client) ReconcileAuthentication(ctx context.Context, credentials Credentials, jellyfinExternalURL string) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fmt.Errorf("Seerr authentication requires Jellyfin administrator credentials")
	}
	if jellyfinExternalURL == "" {
		return fmt.Errorf("Seerr authentication requires the Jellyfin LAN URL")
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

	var jellyfin jellyfinSettings
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/settings/jellyfin", nil, &jellyfin); err != nil {
		return fmt.Errorf("observe Seerr Jellyfin connection: %w", err)
	}
	forgotPasswordURL := strings.TrimRight(jellyfinExternalURL, "/") + "/web/index.html#!/forgotpassword.html"
	if jellyfin.ExternalHostname != jellyfinExternalURL || jellyfin.ForgotPasswordURL != forgotPasswordURL {
		jellyfin.ExternalHostname = jellyfinExternalURL
		jellyfin.ForgotPasswordURL = forgotPasswordURL
		if err := client.doJSON(ctx, http.MethodPost, "/api/v1/settings/jellyfin", jellyfin, nil); err != nil {
			return fmt.Errorf("reconcile Seerr Jellyfin connection: %w", err)
		}
	}

	if _, err := client.authenticateLocal(ctx, credentials); err != nil {
		owner, err = client.authenticateJellyfin(ctx, credentials, false)
		if err != nil {
			return err
		}
		password := map[string]string{"newPassword": credentials.Password}
		path := fmt.Sprintf("/api/v1/user/%d/settings/password", owner.ID)
		if err := client.doJSON(ctx, http.MethodPost, path, password, nil); err != nil {
			return fmt.Errorf("establish Seerr emergency local administrator: %w", err)
		}
		if _, err := client.authenticateLocal(ctx, credentials); err != nil {
			return fmt.Errorf("verify Seerr emergency local administrator: %w", err)
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
	if _, err := client.authenticateLocal(ctx, credentials); err != nil {
		return fmt.Errorf("verify Seerr emergency local administrator: %w", err)
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
