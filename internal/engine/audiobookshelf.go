package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
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
	ids, err := exec.CommandContext(ctx, "docker", "network", "ls", "-q", "--filter", "label=com.docker.compose.project="+projectName, "--filter", "label=com.docker.compose.network="+topology.AudiobookshelfSetupNetwork).Output()
	if err != nil {
		return fmt.Errorf("inspect Audiobookshelf setup network: %w", err)
	}
	for _, id := range strings.Fields(string(ids)) {
		if err := exec.CommandContext(ctx, "docker", "network", "rm", id).Run(); err != nil {
			return fmt.Errorf("remove Audiobookshelf setup network: %w", err)
		}
	}
	return client.ReconcileLibrary(ctx, creds)
}

func audioPublishedClient(ctx context.Context, plan Plan, private bool) (audiobookshelf.Client, error) {
	output, err := runDockerCompose(ctx, plan, "port", "audiobookshelf", "80")
	if err != nil {
		return audiobookshelf.Client{}, fmt.Errorf("resolve Audiobookshelf port: %w", err)
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(string(output)))
	if err != nil {
		return audiobookshelf.Client{}, fmt.Errorf("invalid Audiobookshelf published address: %w", err)
	}
	if private && host != "127.0.0.1" {
		return audiobookshelf.Client{}, fmt.Errorf("Audiobookshelf setup requires a loopback port")
	}
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return audiobookshelf.Client{URL: "http://" + net.JoinHostPort(host, port), HTTP: &http.Client{Timeout: 10 * time.Second}}, nil
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
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		initialized, err := client.Initialized(ctx)
		if err == nil {
			return client, initialized, nil
		}
		select {
		case <-ctx.Done():
			return client, false, fmt.Errorf("wait for Audiobookshelf status: %w", err)
		case <-time.After(time.Second):
		}
	}
}
