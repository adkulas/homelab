package shelfarr

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

// SettingsMatch compares visible settings only. Shelfarr masks the token
// value, and its authenticated write compares the submitted secret on apply.
func (c *Client) SettingsMatch(ctx context.Context, password string, hasToken bool) (bool, error) {
	if err := c.signIn(ctx, password); err != nil {
		return false, err
	}
	status, _, body, err := c.request(ctx, http.MethodGet, settingsPath, "", nil)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("Shelfarr settings returned HTTP %d", status)
	}
	page, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("read Shelfarr settings form")
	}
	expected := map[string]string{
		"settings_open_library_enabled":     "true",
		"settings_immediate_search_enabled": "true",
		"settings_metadata_source":          "auto",
	}
	if hasToken {
		expected["settings_hardcover_enabled"] = "true"
		expected["settings_hardcover_api_token"] = "********"
	}
	for node := range page.Descendants() {
		id, _ := attribute(node, "id")
		want, exists := expected[id]
		if !exists {
			continue
		}
		if settingValue(node) != want {
			return false, nil
		}
		delete(expected, id)
	}
	if len(expected) != 0 {
		return false, fmt.Errorf("Shelfarr settings form is missing managed fields")
	}
	return true, nil
}

func settingValue(node *html.Node) string {
	if node.Data == "select" {
		for option := range node.Descendants() {
			if _, selected := attribute(option, "selected"); selected {
				value, _ := attribute(option, "value")
				return value
			}
		}
	}
	fieldType, _ := attribute(node, "type")
	if fieldType == "checkbox" {
		if _, checked := attribute(node, "checked"); checked {
			return "true"
		}
		return "false"
	}
	value, _ := attribute(node, "placeholder")
	return value
}

func attribute(node *html.Node, key string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val, true
		}
	}
	return "", false
}
