package seerr

import (
	"context"
	"fmt"
)

// AuthenticationDrift observes initialized settings with the existing local
// administrator. Jellyfin login can initialize Seerr, so plan must not use it.
func (client *Client) AuthenticationDrift(ctx context.Context, credentials Credentials) (bool, error) {
	var public struct {
		Initialized     bool `json:"initialized"`
		MediaServerType int  `json:"mediaServerType"`
	}
	if err := client.doJSON(ctx, "GET", "/api/v1/settings/public", nil, &public); err != nil {
		return false, err
	}
	if !public.Initialized || public.MediaServerType == 4 {
		return true, nil
	}
	if err := client.VerifyLocalAdministrator(ctx, credentials); err != nil {
		// apply can restore local login with the Jellyfin administrator session.
		return true, nil
	}
	var main mainSettings
	if err := client.doJSON(ctx, "GET", "/api/v1/settings/main", nil, &main); err != nil {
		return false, err
	}
	return !main.LocalLogin || !main.MediaServerLogin || !main.NewJellyfinLogin || main.DefaultPermissions != requestPermission, nil
}

// UnknownDestinations requires an authenticated administrator session.
func (client *Client) UnknownDestinations(ctx context.Context) ([]string, error) {
	var radarr []radarrDestination
	if err := client.doJSON(ctx, "GET", "/api/v1/settings/radarr", nil, &radarr); err != nil {
		return nil, err
	}
	var sonarr []sonarrDestination
	if err := client.doJSON(ctx, "GET", "/api/v1/settings/sonarr", nil, &sonarr); err != nil {
		return nil, err
	}
	var unknown []string
	for _, destination := range radarr {
		if !destination.hasSameEndpoint(internalRadarrDestination("")) {
			unknown = append(unknown, fmt.Sprintf("radarr/%d", destination.ID))
		}
	}
	for _, destination := range sonarr {
		if !destination.hasSameEndpoint(internalSonarrDestination("")) {
			unknown = append(unknown, fmt.Sprintf("sonarr/%d", destination.ID))
		}
	}
	return unknown, nil
}
