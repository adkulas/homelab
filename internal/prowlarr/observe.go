package prowlarr

import (
	"context"
	"fmt"
)

func (client *Client) UnknownResources(ctx context.Context) ([]string, error) {
	var indexers []indexer
	if err := client.getJSON(ctx, "/api/v1/indexer", &indexers); err != nil {
		return nil, err
	}
	var applications []application
	if err := client.getJSON(ctx, "/api/v1/applications", &applications); err != nil {
		return nil, err
	}
	var unknown []string
	for _, item := range indexers {
		if item.DefinitionName != internetArchiveDefinition || item.Name != "Internet Archive" {
			unknown = append(unknown, fmt.Sprintf("indexer/%d", item.ID))
		}
	}
	for _, item := range applications {
		if !((item.Name == "Radarr" && item.Implementation == "Radarr") || (item.Name == "Sonarr" && item.Implementation == "Sonarr")) {
			unknown = append(unknown, fmt.Sprintf("application/%d", item.ID))
		}
	}
	return unknown, nil
}
