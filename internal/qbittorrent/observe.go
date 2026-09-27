package qbittorrent

import "context"

func (client *Client) UnknownResources(ctx context.Context) ([]string, error) {
	var categories map[string]Category
	if err := client.getJSON(ctx, "/api/v2/torrents/categories", &categories); err != nil {
		return nil, err
	}
	var unknown []string
	for name := range categories {
		if name != "movies" && name != "series" {
			unknown = append(unknown, "category/"+name)
		}
	}
	return unknown, nil
}
