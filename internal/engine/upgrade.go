package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/prowlarr"
	"gopkg.in/yaml.v3"
)

type UpgradeCheck struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Log       string `json:"log,omitempty"`
}

type UpgradePreflight struct {
	Project          string            `json:"project"`
	BackupRoot       string            `json:"backupRoot"`
	Endpoint         string            `json:"dockerEndpoint"`
	Daemon           string            `json:"dockerDaemon"`
	Machine          string            `json:"machine"`
	ConfigDigest     string            `json:"configDigest"`
	VersionsDigest   string            `json:"versionsDigest"`
	Running          map[string]string `json:"runningImages"`
	Candidate        map[string]string `json:"candidateImages"`
	SeriesAutomation bool              `json:"seriesAutomation"`
	Checks           []UpgradeCheck    `json:"checks"`
}

// InspectUpgrade only observes the selected local deployment.
func InspectUpgrade(ctx context.Context, request PlanRequest, hostname, baseline, movie, series string) (UpgradePreflight, error) {
	host, err := os.Hostname()
	if err != nil || hostname == "" || host != hostname {
		return UpgradePreflight{}, fmt.Errorf("upgrade requires the selected machine's hostname to match --hostname")
	}
	machine, daemon, endpoint, err := destroyHostIdentity(ctx, nil)
	if err != nil {
		return UpgradePreflight{}, err
	}
	declared, err := request.load()
	if err != nil {
		return UpgradePreflight{}, err
	}
	if err := declared.ValidateEnvironment(request.environment); err != nil {
		return UpgradePreflight{}, err
	}
	env := declared.Spec.Environments[request.environment]
	pins, err := config.LoadVersions(request.versionsPath)
	if err != nil {
		return UpgradePreflight{}, err
	}
	plan, err := (localEngine{}).Plan(ctx, request)
	if err != nil {
		return UpgradePreflight{}, err
	}
	report := UpgradePreflight{
		Project: env.ProjectName, BackupRoot: env.BackupRoot, Endpoint: endpoint, Daemon: daemon, Machine: machine,
		ConfigDigest: request.configurationDigest(), VersionsDigest: fileDigest(request.versionsPath),
		Running: map[string]string{}, Candidate: map[string]string{}, SeriesAutomation: seriesAutomationEnabled(env),
	}
	var selected composeProject
	if err := yaml.Unmarshal(plan.Compose(), &selected); err != nil {
		return report, err
	}
	for name := range selected.Services {
		report.Candidate[name] = pins.Images[name]
	}
	add := func(name, detail string, err error, retryable bool) {
		check := UpgradeCheck{Name: name, Status: "pass", Detail: detail}
		if err != nil {
			check.Status, check.Detail, check.Retryable = "fail", err.Error(), retryable
		}
		report.Checks = append(report.Checks, check)
	}
	ids, err := destroyDocker(ctx, "ps", "--filter", "label=com.docker.compose.project="+env.ProjectName, "--format", "{{.ID}}")
	if err != nil {
		return report, err
	}
	for _, id := range strings.Fields(string(ids)) {
		output, err := destroyDocker(ctx, "inspect", "--format", `{{index .Config.Labels "com.docker.compose.service"}} {{.Config.Image}}`, id)
		if err != nil {
			return report, err
		}
		fields := strings.Fields(string(output))
		if len(fields) != 2 || fields[0] == "" || report.Running[fields[0]] != "" {
			return report, fmt.Errorf("ambiguous running Compose service")
		}
		report.Running[fields[0]] = fields[1]
	}
	expected := report.Candidate
	if baseline != "" {
		old, err := config.LoadVersions(baseline)
		if err != nil {
			return report, err
		}
		expected = old.Images
	}
	var mismatches []string
	for name := range report.Candidate {
		if report.Running[name] == "" || report.Running[name] != expected[name] {
			mismatches = append(mismatches, name)
		}
	}
	sort.Strings(mismatches)
	if len(mismatches) != 0 && baseline != "" {
		add("deployed pins", "", fmt.Errorf("baseline differs for %s", strings.Join(mismatches, ", ")), false)
	} else if len(mismatches) != 0 {
		add("deployed pins", "candidate differs for "+strings.Join(mismatches, ", "), nil, false)
	} else {
		add("deployed pins", "match expected images", nil, false)
	}
	if _, err := decryptSelectedEnvironmentSecrets(ctx, request.configPath, env); err != nil {
		add("credentials", "", fmt.Errorf("check decryption and scope of %s", selectedSecretPath(request.configPath, env)), false)
	} else {
		add("credentials", "scope and decryption verified", nil, false)
	}
	add("backup destination", env.BackupRoot, inspectUpgradeBackupRoot(ctx, env.BackupRoot, env.DataRoot), false)
	prowlarrCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	key, err := waitForServiceAPIKey(prowlarrCtx, plan, "prowlarr", "Prowlarr", 8*time.Second)
	if err != nil {
		add("Prowlarr identity", "", err, true)
	} else {
		base := "http://" + environmentAddress(declared.Spec.Defaults.LANBindAddress, env.Ports.Prowlarr)
		client := &http.Client{Timeout: 12 * time.Second}
		status, err := prowlarr.New(base, key, client).InternetArchiveStatus(prowlarrCtx)
		client.Timeout = 90 * time.Second
		add("Prowlarr identity", status, err, false)
		if movie != "" {
			f, _, err := loadLegalMovieFixture(movie)
			if err == nil {
				err = checkUpgradeRelease(ctx, client, base, key, f.Spec.ReleaseTitle)
			}
			add("legal movie fixture", "unique exact Internet Archive release", err, true)
		}
		if series != "" {
			f, _, err := loadLegalSeriesFixture(series)
			if err == nil {
				err = checkUpgradeRelease(ctx, client, base, key, f.Spec.ReleaseTitle)
			}
			add("legal series fixture", "unique exact Internet Archive release", err, true)
		}
	}
	return report, nil
}

func inspectUpgradeBackupRoot(ctx context.Context, root, dataRoot string) error {
	prior, err := filepath.EvalSymlinks(dataRoot)
	if err != nil {
		return fmt.Errorf("resolve data root: %w", err)
	}
	parent := root
	for {
		info, err := os.Lstat(parent)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("backup destination ancestor is not a directory: %s", parent)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent = filepath.Dir(parent)
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	suffix, err := filepath.Rel(parent, root)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(prior, filepath.Join(resolved, suffix))
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
		return fmt.Errorf("backup destination resolves inside the data root")
	}
	if err := exec.CommandContext(ctx, "test", "-w", parent).Run(); err != nil {
		return fmt.Errorf("backup destination is not writable: %s", parent)
	}
	return nil
}

func checkUpgradeRelease(ctx context.Context, client *http.Client, base, key, title string) error {
	request := func(path string, destination any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Api-Key", key)
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Internet Archive query did not return a response")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("Internet Archive query returned HTTP %d", response.StatusCode)
		}
		return json.NewDecoder(response.Body).Decode(destination)
	}
	var indexers []struct {
		ID             int
		DefinitionName string
		Enable         bool
	}
	if err := request("/api/v1/indexer", &indexers); err != nil {
		return err
	}
	for _, indexer := range indexers {
		if indexer.DefinitionName != "internetarchive" || !indexer.Enable {
			continue
		}
		query := url.Values{"indexerIds": {strconv.Itoa(indexer.ID)}, "type": {"search"}, "query": {title}}
		var releases []struct {
			Title, Protocol string
			IndexerID       int
			Seeders         int
		}
		if err := request("/api/v1/search?"+query.Encode(), &releases); err != nil {
			return err
		}
		matches := 0
		for _, release := range releases {
			if release.IndexerID == indexer.ID && release.Protocol == "torrent" && release.Seeders > 0 && strings.EqualFold(release.Title, title) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("found %d seeded exact releases for %q", matches, title)
		}
		return nil
	}
	return fmt.Errorf("Internet Archive indexer is unavailable")
}
