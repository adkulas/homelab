package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/engine"
)

const upgradeSchema = "homelab.media-stack/upgrade-evidence/v1alpha1"

type upgradeArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type upgradeEvidence struct {
	Schema          string                  `json:"schemaVersion"`
	Site            string                  `json:"site"`
	Instance        string                  `json:"instance"`
	Host            string                  `json:"host"`
	Hostname        string                  `json:"hostname"`
	Environment     string                  `json:"environment"`
	Commit          string                  `json:"commit"`
	Preflight       engine.UpgradePreflight `json:"preflight"`
	Verification    upgradeArtifact         `json:"verification"`
	Backup          upgradeArtifact         `json:"backup"`
	JellyfinRefresh *upgradeArtifact        `json:"jellyfinRefresh,omitempty"`
	Checks          []engine.UpgradeCheck   `json:"checks"`
	Complete        bool                    `json:"complete"`
}

func runUpgrade(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 || (arguments[0] != "preflight" && arguments[0] != "evidence") {
		return fmt.Errorf("use upgrade preflight or upgrade evidence")
	}
	collect := arguments[0] == "evidence"
	flags := flag.NewFlagSet("upgrade "+arguments[0], flag.ContinueOnError)
	selection := deploymentFlags(flags)
	env := flags.String("environment", "", "selected Environment")
	hostname := flags.String("hostname", "", "verified operating-system hostname of the assigned Host")
	commit := flags.String("commit", "", "expected full candidate Git SHA")
	baseline := flags.String("baseline-versions", "", "deployed revision's versions.yaml")
	movie := flags.String("legal-fixture", "", "legal movie fixture")
	series := flags.String("legal-series-fixture", "", "legal series fixture")
	recovery := flags.String("recovery-fixture", "", "legal Staging recovery fixture")
	refresh := flags.String("jellyfin-refresh-evidence", "", "optional completed native Jellyfin refresh evidence")
	dir := flags.String("evidence-dir", "", "new private directory for the evidence manifest and logs")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*output != "human" && *output != "json") || *commit == "" || *hostname == "" {
		return fmt.Errorf("supply --commit and --hostname with valid upgrade options")
	}
	if collect && (*env != "staging" || *dir == "" || *movie == "" || *series == "") {
		return fmt.Errorf("upgrade evidence requires Staging, a new --evidence-dir, both legal fixtures, and deployed candidate images")
	}
	if !collect && (*dir != "" || *recovery != "" || *refresh != "") {
		return fmt.Errorf("evidence directory and recovery fixture belong to upgrade evidence")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := engine.RepositoryRoot(wd)
	if err != nil {
		return err
	}
	if err := checkUpgradeCheckout(ctx, root, *commit); err != nil {
		return err
	}
	deployment, err := selection.resolve(ctx, wd, *env, "")
	if err != nil {
		return err
	}
	request, err := engine.NewPlanRequest(wd, *env, "")
	if err != nil {
		return err
	}
	if collect {
		if err := validateEvidenceLocation(*dir, root, deployment.Declared.Spec.Environments[*env].BackupRoot, deployment.Declared.Spec.Environments[*env].DataRoot); err != nil {
			return err
		}
		if deployment.Declared.Spec.Environments[*env].SeriesAutomation != nil &&
			deployment.Declared.Spec.Environments[*env].SeriesAutomation.Enabled && *recovery == "" {
			return fmt.Errorf("upgrade evidence requires --recovery-fixture when series automation is enabled")
		}
	}
	expected := *baseline
	var old map[string]string
	if collect && expected != "" {
		versions, err := config.LoadVersions(expected)
		if err != nil {
			return err
		}
		old, expected = versions.Images, ""
	}
	preflight, err := engine.InspectUpgrade(ctx, request.WithDeployment(deployment), *hostname, expected, *movie, *series)
	if err != nil {
		return operationalFailure{cause: err}
	}
	report := upgradeEvidence{Schema: upgradeSchema, Site: *selection.site, Instance: *selection.instance, Host: deployment.HostName, Hostname: *hostname, Environment: *env, Commit: *commit, Preflight: preflight, Checks: preflight.Checks}
	if !collect {
		return printUpgradeReport(report, *output, "")
	}
	if err := os.Mkdir(*dir, 0700); err != nil {
		return err
	}
	absolute, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if !maps.Equal(preflight.Running, preflight.Candidate) {
		report.Checks = append(report.Checks, engine.UpgradeCheck{Name: "candidate deployment", Status: "fail", Detail: "running service images differ from candidate pins"})
	}
	if !upgradeFailed(report.Checks) {
		report.collect(ctx, root, absolute, *movie, *series, *recovery, old)
		if *refresh != "" && !upgradeFailed(report.Checks) {
			report.attachRefresh(*refresh)
		}
		if err := checkUpgradeCheckout(ctx, root, *commit); err != nil {
			report.Checks = append(report.Checks, engine.UpgradeCheck{Name: "candidate checkout", Status: "fail", Detail: err.Error()})
		}
		report.Complete = !upgradeFailed(report.Checks)
	}
	path := filepath.Join(absolute, "upgrade.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(report); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return printUpgradeReport(report, *output, path)
}

func checkUpgradeCheckout(ctx context.Context, root, commit string) error {
	head, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != commit {
		return fmt.Errorf("checkout HEAD must equal the full candidate --commit")
	}
	if err := exec.CommandContext(ctx, "git", "-C", root, "diff", "--quiet", "HEAD", "--").Run(); err != nil {
		return fmt.Errorf("candidate checkout contains tracked changes")
	}
	return nil
}

func validateEvidenceLocation(dir string, protectedPaths ...string) error {
	parent, err := filepath.Abs(filepath.Dir(dir))
	if err != nil {
		return err
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	for _, protected := range protectedPaths {
		if resolved, err := filepath.EvalSymlinks(protected); err == nil {
			protected = resolved
		}
		relative, err := filepath.Rel(protected, filepath.Join(parent, filepath.Base(dir)))
		if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
			return fmt.Errorf("evidence directory must be outside the checkout, data root and backup root")
		}
	}
	return nil
}

func (r *upgradeEvidence) collect(ctx context.Context, root, dir, movie, series, recovery string, old map[string]string) {
	binary, err := os.Executable()
	if err != nil {
		r.Checks = append(r.Checks, engine.UpgradeCheck{Name: "CLI executable", Status: "fail", Detail: err.Error()})
		return
	}
	target := []string{"--site", r.Site, "--instance", r.Instance, "--environment", r.Environment}
	runCLI := func(name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, binary, append(append([]string{name}, target...), args...)...)
	}
	for _, name := range slices.Sorted(maps.Keys(r.Preflight.Candidate)) {
		if exec.CommandContext(ctx, "docker", "image", "inspect", r.Preflight.Candidate[name]).Run() == nil {
			r.Checks = append(r.Checks, engine.UpgradeCheck{Name: "image " + name, Status: "pass", Detail: "immutable reference already present"})
			continue
		}
		if !r.run("pull "+name, filepath.Join(dir, "pull-"+name+".log"), exec.CommandContext(ctx, "docker", "pull", r.Preflight.Candidate[name]), nil) {
			return
		}
	}
	if !r.run("doctor", filepath.Join(dir, "doctor.json"), runCLI("doctor", "--output", "json"), nil) {
		return
	}
	if !r.runLiveChecks(ctx, root, dir, old) {
		return
	}
	verify := []string{"--suite", "promotion", "--legal-fixture", movie, "--legal-series-fixture", series, "--output", "json"}
	if recovery != "" {
		verify = append(verify, "--recovery-fixture", recovery)
	}
	path := filepath.Join(dir, "verification.json")
	if !r.run("Promotion verification", path, runCLI("verify", verify...), func() error {
		var verification engine.VerifyReport
		if err := decodeUpgradeJSON(path, &verification); err != nil {
			return err
		}
		if verification.Failed() || verification.ConfigDigest != r.Preflight.ConfigDigest || verification.VersionsDigest != r.Preflight.VersionsDigest {
			return fmt.Errorf("verification does not match preflight")
		}
		var err error
		r.Verification, err = readUpgradeArtifact(path)
		return err
	}) {
		return
	}
	path = filepath.Join(dir, "backup.json")
	r.run("verified Staging backup", path, runCLI("backup", "--label", "verified-upgrade", "--output", "json"), func() error {
		var backup engine.BackupReport
		if err := decodeUpgradeJSON(path, &backup); err != nil {
			return err
		}
		if !backup.Complete || backup.ManifestPath == "" || backup.ConfigDigest != r.Preflight.ConfigDigest || backup.VersionDigest != r.Preflight.VersionsDigest {
			return fmt.Errorf("backup is incomplete")
		}
		var err error
		r.Backup, err = readUpgradeArtifact(backup.ManifestPath)
		return err
	})
}

func (r *upgradeEvidence) runLiveChecks(ctx context.Context, root, dir string, old map[string]string) bool {
	changed := func(service string) bool { return old == nil || old[service] != r.Preflight.Candidate[service] }
	checks := []struct {
		enabled       bool
		flag, pattern string
	}{
		{true, "MEDIA_STACK_LIVE_JELLYFIN_REFRESH", "TestDisposablePinnedJellyfinLibraryRefresh"},
		{changed("profilarr"), "MEDIA_STACK_LIVE_PROFILARR", "TestDisposablePinnedProfilarr(Apply|Recovery)"},
		{changed("seerr") || changed("jellyfin"), "MEDIA_STACK_LIVE_SEERR", "TestDisposableSeerrAuthenticatesHouseholdAndEmergencyLocalUsers"},
		{r.Preflight.SeriesAutomation && (changed("sonarr") || changed("cleanuparr") || changed("houndarr") || changed("qbittorrent")), "MEDIA_STACK_LIVE_COMPANIONS", "TestDisposablePinnedSeriesCompanions"},
	}
	var patterns, environment []string
	for _, check := range checks {
		if check.enabled {
			patterns = append(patterns, check.pattern)
			environment = append(environment, check.flag+"=1")
		}
	}
	command := exec.CommandContext(ctx, "go", "test", "./tests/acceptance", "-run", "^("+strings.Join(patterns, "|")+")$", "-count=1", "-timeout=45m", "-v")
	command.Dir = root
	command.Env = append(os.Environ(), environment...)
	return r.run("pinned live checks", filepath.Join(dir, "live-checks.log"), command, nil)
}

func (r *upgradeEvidence) attachRefresh(path string) {
	check := engine.UpgradeCheck{Name: "Jellyfin refresh evidence", Status: "pass"}
	absolute, err := filepath.Abs(path)
	if err == nil {
		var artifact upgradeArtifact
		artifact, err = readUpgradeArtifact(absolute)
		if err == nil {
			r.JellyfinRefresh = &artifact
		}
	}
	if err != nil {
		check.Status, check.Detail = "fail", err.Error()
	}
	r.Checks = append(r.Checks, check)
}

func (r *upgradeEvidence) run(name, path string, command *exec.Cmd, validate func() error) bool {
	check := engine.UpgradeCheck{Name: name, Status: "pass", Log: path}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		command.Stdout, command.Stderr = file, file
		err = command.Run()
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil && validate != nil {
		err = validate()
	}
	if err != nil {
		check.Status, check.Detail = "fail", err.Error()
		if name == "doctor" || name == "Promotion verification" {
			var report struct{ Diagnostics []engine.Diagnostic }
			file, openErr := os.Open(path)
			if openErr == nil {
				defer file.Close()
			}
			if openErr == nil && json.NewDecoder(file).Decode(&report) == nil {
				var codes []string
				check.Retryable = true
				for _, diagnostic := range report.Diagnostics {
					if diagnostic.Status == "fail" {
						codes = append(codes, diagnostic.Code)
						check.Retryable = check.Retryable && diagnostic.Retryable
					}
				}
				if len(codes) > 0 {
					check.Detail = strings.Join(codes, ", ")
				} else {
					check.Retryable = false
				}
			}
		}
	}
	r.Checks = append(r.Checks, check)
	return err == nil
}

func upgradeFailed(checks []engine.UpgradeCheck) bool {
	return slices.ContainsFunc(checks, func(check engine.UpgradeCheck) bool { return check.Status == "fail" })
}

func printUpgradeReport(report upgradeEvidence, output, path string) error {
	failed := upgradeFailed(report.Checks)
	if output == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Printf("%s/%s/%s on %s (%s) at %s\n", report.Site, report.Instance, report.Environment, report.Host, report.Hostname, report.Commit)
		for _, check := range report.Checks {
			fmt.Printf("%s %s", check.Status, check.Name)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			if check.Status == "fail" && check.Retryable {
				fmt.Print(" (retryable)")
			}
			if check.Status == "fail" && check.Log != "" {
				fmt.Printf(" [%s]", check.Log)
			}
			fmt.Println()
		}
		if path != "" {
			fmt.Println(path)
		}
	}
	if failed {
		return operationalFailure{cause: fmt.Errorf("upgrade checks failed")}
	}
	return nil
}

func decodeUpgradeJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func readUpgradeArtifact(path string) (upgradeArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return upgradeArtifact{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return upgradeArtifact{}, err
	}
	return upgradeArtifact{Path: path, SHA256: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}

func upgradePromotionPaths(ctx context.Context, path, wd, site, instance, host string) (string, string, string, error) {
	var manifest upgradeEvidence
	if err := decodeUpgradeJSON(path, &manifest); err != nil {
		return "", "", "", err
	}
	hostname, err := os.Hostname()
	if err != nil || manifest.Schema != upgradeSchema || !manifest.Complete || upgradeFailed(manifest.Checks) ||
		manifest.Environment != "staging" || manifest.Site != site || manifest.Instance != instance || manifest.Host != host || manifest.Hostname != hostname {
		return "", "", "", fmt.Errorf("upgrade evidence must be complete and match the assigned Staging target")
	}
	if err := checkUpgradeCheckout(ctx, wd, manifest.Commit); err != nil {
		return "", "", "", err
	}
	references := []upgradeArtifact{manifest.Verification, manifest.Backup}
	refresh := ""
	if manifest.JellyfinRefresh != nil {
		references = append(references, *manifest.JellyfinRefresh)
		refresh = manifest.JellyfinRefresh.Path
	}
	for _, reference := range references {
		artifact, err := readUpgradeArtifact(reference.Path)
		if err != nil || artifact != reference {
			return "", "", "", fmt.Errorf("upgrade evidence artifact is missing or changed: %s", reference.Path)
		}
	}
	return manifest.Verification.Path, manifest.Backup.Path, refresh, nil
}
