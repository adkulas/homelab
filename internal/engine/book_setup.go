package engine

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

func removeSetupNetwork(ctx context.Context, projectName, network string) error {
	ids, err := exec.CommandContext(ctx, "docker", "network", "ls", "-q", "--filter", "label=com.docker.compose.project="+projectName, "--filter", "label=com.docker.compose.network="+network).Output()
	if err != nil {
		return fmt.Errorf("inspect %s network: %w", network, err)
	}
	for _, id := range strings.Fields(string(ids)) {
		if err := exec.CommandContext(ctx, "docker", "network", "rm", id).Run(); err != nil {
			return fmt.Errorf("remove %s network: %w", network, err)
		}
	}
	return nil
}

func publishedWebAddress(ctx context.Context, plan Plan, service string, private bool) (string, error) {
	output, err := runDockerCompose(ctx, plan, "port", service, "80")
	if err != nil {
		return "", fmt.Errorf("resolve %s port: %w", service, err)
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(string(output)))
	if err != nil {
		return "", fmt.Errorf("invalid %s published address: %w", service, err)
	}
	if private && host != "127.0.0.1" {
		return "", fmt.Errorf("%s setup requires a loopback port", service)
	}
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func waitForBookService(ctx context.Context, subject string, probe func(context.Context) (bool, error)) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		state, err := probe(ctx)
		if err == nil {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("wait for %s: %w", subject, err)
		case <-time.After(time.Second):
		}
	}
}
