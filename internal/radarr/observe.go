package radarr

import (
	"context"
	"fmt"
)

// UnknownResources inventories configuration outside this adapter's declared identities.
func (client *Client) UnknownResources(ctx context.Context) ([]string, error) {
	var roots []rootFolder
	if err := client.getJSON(ctx, "/api/v3/rootfolder", &roots); err != nil {
		return nil, err
	}
	var clients []downloadClient
	if err := client.getJSON(ctx, "/api/v3/downloadclient", &clients); err != nil {
		return nil, err
	}
	var unknown []string
	for _, root := range roots {
		if root.Path != movieLibraryRoot {
			unknown = append(unknown, "rootfolder/"+root.Path)
		}
	}
	for _, item := range clients {
		if item.Name != "qBittorrent" || item.Implementation != "QBittorrent" {
			unknown = append(unknown, fmt.Sprintf("downloadclient/%d", item.ID))
		}
	}
	return unknown, nil
}
