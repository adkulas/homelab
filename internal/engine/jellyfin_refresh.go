package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/jellyfin"
	"github.com/adkulas/homelab/internal/radarr"
	"github.com/adkulas/homelab/internal/sonarr"
)

const jellyfinLibraryMarkers = `for dir in /data/media/movies /data/media/series; do marker="$dir/media-stack-library.keep"; [ ! -L "$marker" ]; if [ ! -f "$marker" ]; then if [ "$1" = check ]; then printf '%s\n' 'missing-marker'; else : > "$marker"; fi; fi; done`

func ensureJellyfinLibraryMarkers(ctx context.Context, plan Plan, defaults config.Defaults, environment config.Environment) error {
	// The pinned Jellyfin omits empty physical roots from its watcher set.
	// Radarr can write the markers with the declared media owner identity.
	identity := fmt.Sprintf("%d:%d", defaults.RuntimeUID, defaults.RuntimeGID)
	args := []string{"exec", "-T", "--user", identity, "radarr", "sh", "-euC", "-c", jellyfinLibraryMarkers, "media-stack-library-markers"}
	output, err := runDockerCompose(ctx, plan, append(args, "check")...)
	if err != nil {
		return fmt.Errorf("inspect Jellyfin library monitoring paths: %w: %s", err, output)
	}
	if !strings.Contains(string(output), "missing-marker\n") {
		return nil
	}
	if err := markJellyfinMonitorReload(jellyfinMonitorReloadMarker(environment)); err != nil {
		return err
	}
	if output, err := runDockerCompose(ctx, plan, append(args, "create")...); err != nil {
		return fmt.Errorf("prepare Jellyfin library monitoring paths: %w: %s", err, output)
	}
	return nil
}

func jellyfinMonitorReloadMarker(environment config.Environment) string {
	return filepath.Join(environmentRuntimeSecretDirectory(environment), "jellyfin-monitor-reload")
}

func markJellyfinMonitorReload(path string) error {
	if err := writeSecretAtomic(path, []byte("pending\n")); err != nil {
		return fmt.Errorf("record pending Jellyfin monitor reload: %w", err)
	}
	return nil
}

func reconcileJellyfinRefresh(ctx context.Context, plan Plan, request PlanRequest, defaults config.Defaults, environment config.Environment, server *jellyfin.Client, movies *radarr.Client, series *sonarr.Client, secrets environmentSecrets) error {
	scanMinutes, err := defaults.JellyfinRecoveryScanMinutes(request.environment)
	if err != nil {
		return err
	}
	reloadMarker := jellyfinMonitorReloadMarker(environment)
	prepareReload := func() error {
		return markJellyfinMonitorReload(reloadMarker)
	}
	if err := waitForJellyfinLibraries(ctx, server, secrets.Jellyfin, 120*time.Second, prepareReload); err != nil {
		return err
	}
	if err := server.ReconcileRefreshPolicy(ctx, secrets.Jellyfin, scanMinutes, prepareReload); err != nil {
		return fmt.Errorf("reconcile Jellyfin library refresh policy: %w", err)
	}
	if _, err := os.Stat(reloadMarker); err == nil {
		if _, err := runDockerCompose(ctx, plan, "restart", "jellyfin"); err != nil {
			return fmt.Errorf("reload Jellyfin real-time library monitors: %w", err)
		}
		if err := waitForRecoveredJellyfin(ctx, server, 120*time.Second); err != nil {
			return err
		}
		if err := os.Remove(reloadMarker); err != nil {
			return fmt.Errorf("clear Jellyfin monitor reload marker: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("observe Jellyfin monitor reload marker: %w", err)
	}

	key, err := server.ReconcileRefreshAPIKey(ctx, secrets.Jellyfin, environment.ProjectName, secrets.JellyfinRefreshKey)
	if err != nil {
		return err
	}
	if key != secrets.JellyfinRefreshKey {
		branch := map[string]string{"apiKey": key}
		if err := patchEncryptedSecrets(ctx, selectedSecretPath(request.configPath, environment), map[string]any{"jellyfinRefresh": branch}, environment); err != nil {
			return fmt.Errorf("persist Jellyfin refresh credential: %w", err)
		}
	}
	if err := movies.ReconcileJellyfinConnection(ctx, key); err != nil {
		return fmt.Errorf("reconcile Radarr Jellyfin refreshes: %w", err)
	}
	if err := series.ReconcileJellyfinConnection(ctx, key); err != nil {
		return fmt.Errorf("reconcile Sonarr Jellyfin refreshes: %w", err)
	}
	return nil
}
