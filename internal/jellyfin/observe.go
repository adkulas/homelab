package jellyfin

import "context"

func (client *Client) UnknownLibraries(ctx context.Context, credentials Credentials) ([]string, error) {
	auth, err := client.authenticate(ctx, credentials)
	if err != nil {
		return nil, err
	}
	var libraries []struct {
		Name string `json:"Name"`
	}
	if err := client.doJSON(ctx, "GET", "/Library/VirtualFolders", nil, auth.AccessToken, &libraries); err != nil {
		return nil, err
	}
	var unknown []string
	for _, library := range libraries {
		if library.Name != "Movie Library" && library.Name != "Series Library" {
			unknown = append(unknown, "library/"+library.Name)
		}
	}
	return unknown, nil
}
