package engine

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/shelfarr"
	"github.com/adkulas/homelab/internal/topology"
)

func startShelfarr(ctx context.Context, plan Plan, projectName, password, token string) error {
	running, err := runDockerCompose(ctx, plan, "ps", "-q", "shelfarr")
	if err != nil {
		return fmt.Errorf("inspect Shelfarr: %w", err)
	}
	initialized := false
	if strings.TrimSpace(string(running)) != "" {
		if address, err := publishedWebAddress(ctx, plan, "shelfarr", false); err == nil {
			client := shelfarr.New(address, shelfarrHTTPClient())
			needsSetup, err := client.NeedsSetup(ctx)
			initialized = err == nil && !needsSetup
		}
	}
	if !initialized {
		setup, err := topology.ShelfarrSetup(plan.Compose())
		if err != nil {
			return err
		}
		client, needsSetup, err := startShelfarrServer(ctx, Plan{compose: setup}, true)
		if err != nil {
			return err
		}
		if needsSetup {
			if err := client.Initialize(ctx, password); err != nil {
				return err
			}
		}
		if err := client.Reconcile(ctx, password, token); err != nil {
			return err
		}
	}
	client, needsSetup, err := startShelfarrServer(ctx, plan, false)
	if err != nil {
		return err
	}
	if needsSetup {
		return fmt.Errorf("Shelfarr is missing its initialized account after startup")
	}
	if err := removeSetupNetwork(ctx, projectName, topology.ShelfarrSetupNetwork); err != nil {
		return err
	}
	if initialized {
		return client.Reconcile(ctx, password, token)
	}
	return nil
}

func shelfarrHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

func startShelfarrServer(ctx context.Context, plan Plan, private bool) (*shelfarr.Client, bool, error) {
	if _, err := runDockerCompose(ctx, plan, "up", "-d", "shelfarr"); err != nil {
		return nil, false, fmt.Errorf("start Shelfarr: %w", err)
	}
	address, err := publishedWebAddress(ctx, plan, "shelfarr", private)
	if err != nil {
		return nil, false, err
	}
	client := shelfarr.New(address, shelfarrHTTPClient())
	needsSetup, err := waitForBookService(ctx, "Shelfarr login", client.NeedsSetup)
	return client, needsSetup, err
}
