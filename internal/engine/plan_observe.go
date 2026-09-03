package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlanAction is an operator-visible difference between Declared Configuration
// and the selected Environment's Compose project.
type PlanAction struct {
	Kind        planActionKind
	Subject     string
	Explanation string
}

type planActionKind string

const (
	planActionCreate   planActionKind = "create"
	planActionDeferred planActionKind = "deferred"
	planActionGuide    planActionKind = "guide"
	planActionRestart  planActionKind = "restart"
	planActionUnknown  planActionKind = "unknown"
	planActionUpdate   planActionKind = "update"
)

type composeProject struct {
	Name     string `yaml:"name"`
	Services map[string]struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
	Networks map[string]any `yaml:"networks"`
	Volumes  map[string]any `yaml:"volumes"`
}

type dockerResource struct {
	Image  string `json:"Image"`
	Labels string `json:"Labels"`
	Name   string `json:"Name"`
	Names  string `json:"Names"`
	State  string `json:"State"`
}

// ObserveTopology reads Docker's project labels without mutating the selected
// Environment. An unavailable Docker observation is deferred so plan remains a
// useful render and validation command before host provisioning.
func ObserveTopology(ctx context.Context, plan Plan) []PlanAction {
	var declared composeProject
	if err := yaml.Unmarshal(plan.Compose(), &declared); err != nil {
		return deferredTopology("rendered Compose could not be inspected")
	}
	declaredHashes, err := composeServiceHashes(ctx, plan.Compose())
	if err != nil {
		return deferredTopology("Docker Compose configuration hashing is unavailable")
	}
	command := exec.CommandContext(ctx, "docker", "ps", "--all", "--filter", "label=com.docker.compose.project="+declared.Name, "--format", "{{json .}}")
	output, err := command.Output()
	if err != nil {
		return deferredTopology("Docker project observation is unavailable")
	}
	networks, err := observeDockerResources(ctx, "network", "ls", "--filter", "label=com.docker.compose.project="+declared.Name, "--format", "{{json .}}")
	if err != nil {
		return deferredTopology("Docker project network observation is unavailable")
	}
	volumes, err := observeDockerResources(ctx, "volume", "ls", "--filter", "label=com.docker.compose.project="+declared.Name, "--format", "{{json .}}")
	if err != nil {
		return deferredTopology("Docker project volume observation is unavailable")
	}

	observed := map[string]dockerResource{}
	unknownContainers := make([]string, 0)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var container dockerResource
		if err := json.Unmarshal(scanner.Bytes(), &container); err != nil {
			return deferredTopology("Docker returned an unsupported project observation")
		}
		service := composeLabel(container.Labels, "com.docker.compose.service")
		if service == "" {
			unknownContainers = append(unknownContainers, firstNonEmpty(container.Names, container.Name))
			continue
		}
		observed[service] = container
	}
	if err := scanner.Err(); err != nil {
		return deferredTopology("Docker project observation could not be read")
	}

	serviceNames := make([]string, 0, len(declared.Services))
	for service := range declared.Services {
		serviceNames = append(serviceNames, service)
	}
	sort.Strings(serviceNames)
	actions := make([]PlanAction, 0)
	for _, service := range serviceNames {
		current, exists := observed[service]
		if !exists {
			actions = append(actions, PlanAction{Kind: planActionCreate, Subject: service, Explanation: "declared service is absent"})
			continue
		}
		if composeLabel(current.Labels, "com.docker.compose.config-hash") != declaredHashes[service] {
			actions = append(actions, PlanAction{Kind: planActionUpdate, Subject: service, Explanation: "rendered configuration differs from Declared Configuration"})
			continue
		}
		if current.State != "running" {
			actions = append(actions, PlanAction{Kind: planActionRestart, Subject: service, Explanation: "declared service is not running"})
		}
	}

	unknown := make([]string, 0)
	for service := range observed {
		if _, declaredService := declared.Services[service]; !declaredService {
			unknown = append(unknown, service)
		}
	}
	sort.Strings(unknown)
	for _, service := range unknown {
		actions = append(actions, unknownAction("service/"+service))
	}
	sort.Strings(unknownContainers)
	for _, container := range unknownContainers {
		actions = append(actions, unknownAction("container/"+container))
	}
	actions = append(actions, resourceActions("network", "com.docker.compose.network", declared.Networks, networks)...)
	actions = append(actions, resourceActions("volume", "com.docker.compose.volume", declared.Volumes, volumes)...)
	return actions
}

func composeServiceHashes(ctx context.Context, compose []byte) (map[string]string, error) {
	command := exec.CommandContext(ctx, "docker", "compose", "-f", "-", "config", "--hash", "*")
	command.Stdin = bytes.NewReader(compose)
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("unsupported Compose hash output")
		}
		hashes[fields[0]] = fields[1]
	}
	return hashes, nil
}

func observeDockerResources(ctx context.Context, arguments ...string) ([]dockerResource, error) {
	output, err := exec.CommandContext(ctx, "docker", arguments...).Output()
	if err != nil {
		return nil, err
	}
	resources := make([]dockerResource, 0)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var resource dockerResource
		if err := json.Unmarshal(scanner.Bytes(), &resource); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, scanner.Err()
}

func resourceActions(kind, label string, declared map[string]any, observed []dockerResource) []PlanAction {
	found := map[string]bool{}
	actions := make([]PlanAction, 0)
	unknown := make([]string, 0)
	for _, resource := range observed {
		name := composeLabel(resource.Labels, label)
		if _, exists := declared[name]; exists {
			found[name] = true
			continue
		}
		unknown = append(unknown, firstNonEmpty(name, resource.Name, resource.Names))
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		actions = append(actions, unknownAction(kind+"/"+name))
	}
	missing := make([]string, 0)
	for name := range declared {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		actions = append(actions, PlanAction{Kind: planActionCreate, Subject: kind + "/" + name, Explanation: "declared resource is absent"})
	}
	return actions
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unidentified"
}

func composeLabel(labels, name string) string {
	for _, label := range strings.Split(labels, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(label), "=")
		if found && key == name {
			return value
		}
	}
	return ""
}

func unknownAction(subject string) PlanAction {
	return PlanAction{Kind: planActionUnknown, Subject: subject, Explanation: "project resource is outside the Service Configuration Contract; retained"}
}

func deferredTopology(explanation string) []PlanAction {
	return []PlanAction{{Kind: planActionDeferred, Subject: "topology", Explanation: explanation}}
}

func FormatPlanActions(actions []PlanAction) []byte {
	var output strings.Builder
	hasChanges := false
	hasDeferred := false
	for _, action := range actions {
		fmt.Fprintf(&output, "# %s %s: %s\n", action.Kind, action.Subject, action.Explanation)
		switch action.Kind {
		case planActionCreate, planActionGuide, planActionRestart, planActionUpdate:
			hasChanges = true
		case planActionDeferred:
			hasDeferred = true
		}
	}
	if !hasChanges && !hasDeferred {
		output.WriteString("# plan: no changes\n")
	}
	return []byte(output.String())
}
