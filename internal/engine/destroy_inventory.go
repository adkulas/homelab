package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

func destroyDocker(ctx context.Context, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "docker", args...).Output()
	// Docker errors can include configuration or environment data; report only the operation.
	if err != nil {
		return nil, fmt.Errorf("Docker %s failed: %w", args[0], err)
	}
	return output, nil
}

func destroyHostIdentity(ctx context.Context, deployment *Deployment) (string, string, string, error) {
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		output, err := destroyDocker(ctx, "context", "inspect", "--format", "{{json .Endpoints.docker.Host}}")
		if err != nil {
			return "", "", "", err
		}
		if err := json.Unmarshal(output, &endpoint); err != nil {
			return "", "", "", fmt.Errorf("ambiguous Docker context")
		}
	}
	if !strings.HasPrefix(endpoint, "unix:///") {
		return "", "", "", fmt.Errorf("destroy requires a local Unix Docker socket; remote Docker contexts are refused")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", "", "", err
	}
	if deployment != nil && deployment.HostName != "" && hostname != deployment.HostName {
		return "", "", "", fmt.Errorf("destroy must run on selected Host %q", deployment.HostName)
	}
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil || len(strings.TrimSpace(string(machine))) < 16 {
		return "", "", "", fmt.Errorf("cannot establish actual Host machine identity")
	}
	output, err := destroyDocker(ctx, "info", "--format", "{{json .}}")
	if err != nil {
		return "", "", "", err
	}
	var daemon struct{ ID, Name string }
	if err := json.Unmarshal(output, &daemon); err != nil || daemon.ID == "" || daemon.Name != hostname {
		return "", "", "", fmt.Errorf("ambiguous Host/daemon targeting: Docker must run on the actual local Host")
	}
	return hostname + ":" + checksum(machine), daemon.ID, endpoint, nil
}

type destroyContainer struct {
	ID              string `json:"Id"`
	Name            string
	Created         string
	Config          struct{ Labels map[string]string }
	State           struct{ Running bool }
	Mounts          []struct{ Type, Name, Source, Destination string }
	NetworkSettings struct {
		Networks map[string]struct{ NetworkID string }
	}
}

type destroyVolume struct {
	Name, CreatedAt, Mountpoint, Driver string
	Labels                              map[string]string
	Options                             map[string]string
}
type destroyNetwork struct {
	ID            string `json:"Id"`
	Name, Created string
	Labels        map[string]string
	Containers    map[string]any
}

func inventoryDestroy(ctx context.Context, plan Plan, environment config.Environment, options DestroyOptions, configPath string) ([]DestroyResource, []string, error) {
	var project composeProject
	if err := yaml.Unmarshal(plan.Compose(), &project); err != nil {
		return nil, nil, err
	}
	sources, err := backupSources(plan.Compose(), environment.ProjectName)
	if err != nil {
		return nil, nil, err
	}
	containers, err := destroyContainers(ctx)
	if err != nil {
		return nil, nil, err
	}
	selected := map[string]bool{}
	// A stopped helper from this Environment's interrupted operation is already
	// owned by its durable journal. It is removed on resume before further work.
	active, activeErr := loadDestroyJournal(filepath.Join(destroyStateDirectory(environment), "active.json"))
	if activeErr != nil && !os.IsNotExist(activeErr) {
		return nil, nil, activeErr
	}
	if activeErr == nil && !active.Completed && active.Plan.Project == environment.ProjectName {
		for _, c := range containers {
			for _, id := range active.Helpers {
				if c.ID == id.ID && !c.State.Running && c.Config.Labels["com.homelab.destroy.operation"] == id.Operation {
					selected[c.ID] = true
				}
			}
		}
	}

	var resources []DestroyResource
	for _, c := range containers {
		labels := c.Config.Labels
		if labels["com.docker.compose.project"] != environment.ProjectName {
			continue
		}
		owner := labels["com.docker.compose.service"]
		if _, ok := project.Services[owner]; !ok {
			continue
		}
		if labels["com.docker.compose.oneoff"] != "False" && labels["com.docker.compose.oneoff"] != "false" {
			return nil, nil, fmt.Errorf("uncertain ownership of container %s", c.Name)
		}
		selected[c.ID] = true
		resources = append(resources, DestroyResource{Kind: DestroyContainer, Name: c.ID, Identity: c.ID, Owner: owner, Running: c.State.Running})
	}
	output, err := destroyDocker(ctx, "network", "ls", "--filter", "label=com.docker.compose.project="+environment.ProjectName, "--format", "{{.ID}}")
	if err != nil {
		return nil, nil, err
	}
	for _, id := range strings.Fields(string(output)) {
		var observed []destroyNetwork
		data, err := destroyDocker(ctx, "network", "inspect", id)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(data, &observed); err != nil || len(observed) != 1 {
			return nil, nil, fmt.Errorf("ambiguous network observation")
		}
		n := observed[0]
		owner := n.Labels["com.docker.compose.network"]
		if _, ok := project.Networks[owner]; !ok {
			continue
		}
		if n.Labels["com.docker.compose.project"] != environment.ProjectName {
			return nil, nil, fmt.Errorf("network ownership changed")
		}
		for consumer := range n.Containers {
			if !selected[consumer] {
				return nil, nil, fmt.Errorf("network %s has an unexpected consumer", n.Name)
			}
		}
		resources = append(resources, DestroyResource{Kind: DestroyNetwork, Name: n.ID, Identity: n.ID, Owner: owner})
	}
	output, err = destroyDocker(ctx, "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, nil, err
	}
	existing := map[string]bool{}
	for _, name := range strings.Fields(string(output)) {
		existing[name] = true
	}
	var missing []string
	for _, source := range sources {
		if !existing[source.dockerVolume] {
			missing = append(missing, source.dockerVolume)
			continue
		}
		var observed []destroyVolume
		data, err := destroyDocker(ctx, "volume", "inspect", source.dockerVolume)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(data, &observed); err != nil || len(observed) != 1 {
			return nil, nil, fmt.Errorf("ambiguous volume observation")
		}
		v := observed[0]
		if v.Labels["com.docker.compose.project"] != environment.ProjectName || v.Labels["com.docker.compose.volume"] != source.volumeName || v.Driver != "local" || len(v.Options) > 0 || v.CreatedAt == "" {
			return nil, nil, fmt.Errorf("uncertain ownership or storage boundary for volume %s", v.Name)
		}
		if options.Volumes {
			if !filepath.IsAbs(v.Mountpoint) {
				return nil, nil, fmt.Errorf("ambiguous volume storage path")
			}
			if err := checkDestroyMounts(v.Mountpoint); err != nil {
				return nil, nil, err
			}
			for _, c := range containers {
				for _, mount := range c.Mounts {
					if (mount.Type == "volume" && mount.Name == v.Name || mount.Type == "bind" && destroyOverlaps(mount.Source, v.Mountpoint)) && !selected[c.ID] {
						return nil, nil, fmt.Errorf("volume %s has an unexpected consumer", v.Name)
					}
				}
			}
			resources = append(resources, DestroyResource{Kind: DestroyVolume, Name: v.Name, Identity: checksum(data), Owner: source.serviceName})
		}
	}
	var paths []destroyPath
	for _, name := range []string{"openvpn_user", "openvpn_password", "profilarr.env"} {
		paths = append(paths, destroyPath{DestroyRuntimeFile, environmentRuntimeSecretDirectory(environment) + "/" + name})
	}
	if options.Downloads {
		paths = append(paths, destroyPath{DestroyDownloads, environment.DataRoot + "/torrents"})
	}
	if options.Media {
		paths = append(paths, destroyPath{DestroyMedia, environment.DataRoot + "/media"})
	}
	if options.Secrets {
		paths = append(paths, destroyPath{DestroySecrets, selectedSecretPath(configPath, environment)})
	}
	// Recovery ciphertext is inventoried even when active application credentials are retained.
	if options.Volumes {
		_, drill := restoreDrillGatePaths(environment.BackupRoot)
		paths = append(paths, destroyPath{DestroyDrillCredentials, drill})
	}
	for _, p := range paths {
		identity, err := destroyPathIdentity(p.path, true)
		if err != nil {
			return nil, nil, err
		}
		resources = append(resources, DestroyResource{Kind: p.kind, Name: p.path, Identity: identity})
	}
	sort.Slice(resources, func(i, j int) bool {

		a, b := resources[i], resources[j]
		if a.Kind.order() != b.Kind.order() {
			return a.Kind.order() < b.Kind.order()
		}
		return a.Name < b.Name
	})
	sort.Strings(missing)
	return resources, missing, nil
}

func destroyContainers(ctx context.Context) ([]destroyContainer, error) {
	output, err := destroyDocker(ctx, "ps", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(output))
	if len(ids) == 0 {
		return nil, nil
	}
	var containers []destroyContainer
	output, err = destroyDocker(ctx, append([]string{"container", "inspect"}, ids...)...)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, fmt.Errorf("cannot decode container identities")
	}
	return containers, nil
}
