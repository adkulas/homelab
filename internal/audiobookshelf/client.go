// Package audiobookshelf manages the initial local account and the shared book library.
package audiobookshelf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	URL  string
	HTTP *http.Client
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c Client) request(ctx context.Context, method, path, token string, body, result any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, input)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// A redirected login or first-run request could send the household password to another origin.
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Audiobookshelf %s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (c Client) Initialized(ctx context.Context) (bool, error) {
	var status struct {
		App    string
		IsInit *bool
	}
	if err := c.request(ctx, http.MethodGet, "/status", "", nil, &status); err != nil {
		return false, err
	}
	if status.App != "audiobookshelf" || status.IsInit == nil {
		return false, fmt.Errorf("unexpected Audiobookshelf status response")
	}
	return *status.IsInit, nil
}

func (c Client) Initialize(ctx context.Context, credentials Credentials) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fmt.Errorf("Audiobookshelf requires household credentials")
	}
	return c.request(ctx, http.MethodPost, "/init", "", map[string]Credentials{"newRoot": credentials}, nil)
}

type folder struct {
	FullPath string `json:"fullPath"`
}
type library struct {
	Name      string   `json:"name"`
	MediaType string   `json:"mediaType"`
	Folders   []folder `json:"folders"`
}

func (c Client) ReconcileLibrary(ctx context.Context, credentials Credentials) error {
	var login struct{ User struct{ AccessToken string } }
	if err := c.request(ctx, http.MethodPost, "/login", "", credentials, &login); err != nil {
		return err
	}
	token := login.User.AccessToken
	if token == "" {
		return fmt.Errorf("Audiobookshelf login did not return an access token")
	}
	found, err := c.hasLibrary(ctx, token)
	if err != nil || found {
		return err
	}
	desired := library{Name: "Audiobooks", MediaType: "book", Folders: []folder{{FullPath: "/audiobooks"}}}
	if err := c.request(ctx, http.MethodPost, "/api/libraries", token, desired, nil); err != nil {
		return err
	}
	found, err = c.hasLibrary(ctx, token)
	if err == nil && !found {
		return fmt.Errorf("Audiobookshelf did not retain the audiobook library")
	}
	return err
}

func (c Client) hasLibrary(ctx context.Context, token string) (bool, error) {
	var list struct{ Libraries []library }
	if err := c.request(ctx, http.MethodGet, "/api/libraries", token, nil, &list); err != nil {
		return false, err
	}
	if list.Libraries == nil {
		return false, fmt.Errorf("Audiobookshelf did not return a library list")
	}
	found := false
	for _, lib := range list.Libraries {
		ownsPath := false
		for _, f := range lib.Folders {
			if f.FullPath == "/audiobooks" {
				ownsPath = true
			}
		}
		if lib.Name != "Audiobooks" && !ownsPath {
			continue
		}
		if found || !ownsPath || len(lib.Folders) != 1 || lib.MediaType != "book" || lib.Name != "Audiobooks" {
			return false, fmt.Errorf("resolve conflicting Audiobookshelf libraries in Settings: Audiobooks must be a book library with only /audiobooks")
		}
		found = true
	}
	return found, nil
}
