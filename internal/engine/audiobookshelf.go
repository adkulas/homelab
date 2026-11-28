package engine

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/audiobookshelf"
	"github.com/adkulas/homelab/internal/topology"
)

func startAudiobookshelf(ctx context.Context, plan Plan, projectName string, creds audiobookshelf.Credentials) error {
	running, err := runDockerCompose(ctx, plan, "ps", "-q", "audiobookshelf")
	if err != nil {
		return fmt.Errorf("inspect Audiobookshelf: %w", err)
	}
	initialized := false
	if strings.TrimSpace(string(running)) != "" {
		client, err := audioPublishedClient(ctx, plan, false)
		if err == nil {
			initialized, _ = client.Initialized(ctx)
		}
	}
	if !initialized {
		setup, err := topology.AudiobookshelfSetup(plan.Compose())
		if err != nil {
			return err
		}
		client, initialized, err := startAudioServer(ctx, Plan{compose: setup}, true)
		if err != nil {
			return err
		}
		if !initialized {
			if err := client.Initialize(ctx, creds); err != nil {
				return err
			}
		}
	}
	client, initialized, err := startAudioServer(ctx, plan, false)
	if err != nil {
		return err
	}
	if !initialized {
		return fmt.Errorf("Audiobookshelf is missing its initialized account after startup")
	}
	// A previous apply may have been interrupted after switching to the LAN port.
	if err := removeSetupNetwork(ctx, projectName, topology.AudiobookshelfSetupNetwork); err != nil {
		return err
	}
	return client.ReconcileLibrary(ctx, creds)
}

func audioPublishedClient(ctx context.Context, plan Plan, private bool) (audiobookshelf.Client, error) {
	address, err := publishedWebAddress(ctx, plan, "audiobookshelf", private)
	if err != nil {
		return audiobookshelf.Client{}, err
	}
	return audiobookshelf.Client{URL: address, HTTP: &http.Client{Timeout: 10 * time.Second}}, nil
}

func startAudioServer(ctx context.Context, plan Plan, private bool) (audiobookshelf.Client, bool, error) {
	var client audiobookshelf.Client
	if _, err := runDockerCompose(ctx, plan, "up", "-d", "audiobookshelf"); err != nil {
		return client, false, fmt.Errorf("start Audiobookshelf: %w", err)
	}
	client, err := audioPublishedClient(ctx, plan, private)
	if err != nil {
		return client, false, err
	}
	initialized, err := waitForBookService(ctx, "Audiobookshelf status", client.Initialized)
	return client, initialized, err
}
