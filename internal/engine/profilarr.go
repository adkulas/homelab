package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/profilarr"
)

func ensureProfilarrPolicy(ctx context.Context, plan Plan, request profilarr.Request, baseURL, key string, verifyPolicy func(context.Context) error) error {
	connections, err := waitForProfilarrConnections(ctx, baseURL, key)
	if err != nil {
		return err
	}
	if profilarr.Complete(connections) {
		return nil
	}
	if len(connections) != 0 {
		return &profilarr.Error{Code: "non_fresh_state", Remedy: "existing connections are partial, duplicated, or unexpected; preserve state and use guided Profilarr setup"}
	}
	output, err := runDockerCompose(ctx, plan, "ps", "--all", "-q", "profilarr")
	if err != nil || len(strings.Fields(string(output))) != 1 {
		return &profilarr.Error{Code: "precondition_failed", Remedy: "cannot identify the selected Profilarr container"}
	}
	request.ContainerID = strings.TrimSpace(string(output))
	request.Verify = func(ctx context.Context) error {
		observed, err := waitForProfilarrConnections(ctx, baseURL, key)
		if err != nil {
			return err
		}
		if !profilarr.Complete(observed) {
			return fmt.Errorf("Profilarr connections did not match the declared topology")
		}
		deadline := time.NewTimer(3 * time.Minute)
		defer deadline.Stop()
		for {
			if err := profilarr.Synchronized(ctx, baseURL, key); err == nil {
				if err := verifyPolicy(ctx); err == nil {
					return nil
				}
			} else {
				var diagnostic *profilarr.Error
				if errors.As(err, &diagnostic) && diagnostic.Code == "synchronization_failure" {
					return err
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return fmt.Errorf("Profilarr policy synchronization did not converge within 3m")
			case <-time.After(time.Second):
			}
		}
	}
	return profilarr.Bootstrap(ctx, request)
}

func waitForProfilarrConnections(ctx context.Context, baseURL, key string) ([]profilarr.Connection, error) {
	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()
	for {
		observed, retry, err := profilarr.Observe(ctx, baseURL, key)
		if err == nil {
			if len(observed) != 0 {
				return observed, nil
			}
			healthy, healthErr := profilarr.Ready(ctx, baseURL, key)
			if healthErr != nil {
				return nil, healthErr
			}
			if healthy {
				return observed, nil
			}
			retry = true
		}
		if !retry {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, &profilarr.Error{Code: "migration_failure", Remedy: "Profilarr did not become ready within 2m; inspect migrations before retrying"}
		case <-time.After(2 * time.Second):
		}
	}
}
