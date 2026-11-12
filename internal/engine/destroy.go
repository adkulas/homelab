package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

const destroySchema = "homelab.media-stack/destroy/v1alpha1"

// DestroyOptions selects independent optional reset actions. Runtime removal is unconditional.
type DestroyOptions struct {
	Volumes   bool `json:"volumes"`
	Downloads bool `json:"downloads"`
	Media     bool `json:"media"`
	Secrets   bool `json:"secrets"`
}

type DestroyRequest struct {
	plan            PlanRequest
	ArtifactPath    string
	Execute         bool
	Confirm         string
	Target          string
	AcknowledgeLoss string
	Resume          string
	Options         DestroyOptions
}

type DestroyPlan struct {
	EffectiveConfigDigest string            `json:"effectiveConfigDigest"`
	SchemaVersion         string            `json:"schemaVersion"`
	Operation             string            `json:"operation"`
	Target                string            `json:"target"`
	Environment           string            `json:"environment"`
	Project               string            `json:"project"`
	Host                  string            `json:"host"`
	Daemon                string            `json:"daemon"`
	Endpoint              string            `json:"endpoint"`
	ConfigDigest          string            `json:"configDigest"`
	VersionDigest         string            `json:"versionDigest"`
	Options               DestroyOptions    `json:"options"`
	CreatedAt             time.Time         `json:"createdAt"`
	ExpiresAt             time.Time         `json:"expiresAt"`
	Resources             []DestroyResource `json:"resources"`
	MissingVolumes        []string          `json:"missingVolumes"`
	Retained              []string          `json:"retained"`
	Preconditions         []string          `json:"preconditions"`
	LossAcknowledgement   string            `json:"lossAcknowledgement,omitempty"`
	Resume                string            `json:"resume,omitempty"`
}

// DestroyResource contains stable identities, never container configuration or credential values.
type DestroyResource struct {
	Kind     DestroyResourceKind `json:"kind"`
	Name     string              `json:"name"`
	Identity string              `json:"identity"`
	Owner    string              `json:"owner,omitempty"`
	Running  bool                `json:"running,omitempty"`
}

type DestroyReport struct {
	Failure     string          `json:"failure,omitempty"`
	Plan        DestroyPlan     `json:"plan"`
	SHA256      string          `json:"sha256"`
	JournalPath string          `json:"journalPath,omitempty"`
	BackupPath  string          `json:"backupPath,omitempty"`
	Completed   bool            `json:"completed"`
	Actions     []DestroyAction `json:"actions,omitempty"`
	Warning     string          `json:"warning"`
}

func NewDestroyRequest(workingDirectory, environment, configPath string, options DestroyOptions) (DestroyRequest, error) {
	if environment != "production" && environment != "staging" {
		return DestroyRequest{}, fmt.Errorf("destroy requires an explicit production or staging Environment")
	}
	if options.Secrets && !options.Volumes {
		return DestroyRequest{}, fmt.Errorf("--secrets requires --volumes")
	}
	plan, err := NewPlanRequest(workingDirectory, environment, configPath)
	return DestroyRequest{plan: plan, Options: options}, err
}

func (r DestroyRequest) WithDeployment(d *Deployment) DestroyRequest {
	r.plan = r.plan.WithDeployment(d)
	return r
}

func (e localEngine) Destroy(ctx context.Context, r DestroyRequest) (DestroyReport, error) {
	if r.ArtifactPath == "" {
		return DestroyReport{}, fmt.Errorf("destroy requires a saved plan path")
	}
	if r.Options.Secrets && !r.Options.Volumes {
		return DestroyReport{}, fmt.Errorf("--secrets requires --volumes")
	}
	if r.Execute {
		return e.executeDestroy(ctx, r)
	}
	if r.Confirm != "" || r.Target != "" || r.AcknowledgeLoss != "" {
		return DestroyReport{}, fmt.Errorf("confirmation flags require --execute")
	}
	p, err := e.previewDestroy(ctx, r)
	if err != nil {
		return DestroyReport{}, err
	}
	absoluteArtifact, err := filepath.Abs(r.ArtifactPath)
	if err != nil {
		return DestroyReport{}, err
	}
	for _, resource := range p.Resources {
		if resource.Kind != DestroyContainer && resource.Kind != DestroyNetwork && resource.Kind != DestroyVolume && destroyOverlaps(absoluteArtifact, resource.Name) {
			return DestroyReport{}, fmt.Errorf("saved plan must be outside selected deletion paths")
		}
	}
	contents, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return DestroyReport{}, err
	}
	contents = append(contents, '\n')
	if err := createPrivateArtifact(r.ArtifactPath, contents); err != nil {
		return DestroyReport{}, err
	}
	return destroyReport(p, checksum(contents)), nil
}

func destroyReport(p DestroyPlan, digest string) DestroyReport {
	warning := "Application-state backups exclude media and downloads. Payload deletion may cause permanent loss. Secret deletion does not revoke provider credentials or erase historical copies."
	return DestroyReport{Plan: p, SHA256: digest, Warning: warning}
}

func (e localEngine) previewDestroy(ctx context.Context, r DestroyRequest) (DestroyPlan, error) {
	refreshed, err := r.plan.refresh(ctx)
	if err != nil {
		return DestroyPlan{}, err
	}
	r.plan = refreshed
	declared, err := r.plan.load()
	if err != nil {
		return DestroyPlan{}, err
	}
	if err := declared.ValidateBackupEnvironment(r.plan.environment); err != nil {
		return DestroyPlan{}, err
	}
	environment := declared.Spec.Environments[r.plan.environment]
	host, daemon, endpoint, err := destroyHostIdentity(ctx, r.plan.deployment)
	if err != nil {
		return DestroyPlan{}, err
	}
	configuration, err := r.plan.configurationBytes()
	if err != nil {
		return DestroyPlan{}, err
	}
	versions, err := os.ReadFile(r.plan.versionsPath)
	if err != nil {
		return DestroyPlan{}, err
	}
	plan, err := e.Plan(ctx, r.plan)
	if err != nil {
		return DestroyPlan{}, err
	}
	now := time.Now().UTC()
	target := r.plan.configPath + "/" + r.plan.environment + "/" + environment.ProjectName
	if scope := environment.SecretScope; scope != nil {
		target = scope.Site + "/" + scope.Instance + "/" + r.plan.environment + "/" + environment.ProjectName
	}
	p := DestroyPlan{EffectiveConfigDigest: checksum(plan.Compose()), SchemaVersion: destroySchema, Operation: "destroy", Target: target, Environment: r.plan.environment, Project: environment.ProjectName, Host: host, Daemon: daemon, Endpoint: endpoint, ConfigDigest: checksum(configuration), VersionDigest: checksum(versions), Options: r.Options, CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute), Resume: r.Resume,
		Retained:      []string{"Declared Configuration and versions", "dataRoot", "historical backups and Promotion evidence", "shared VPN documents and age identities", "other Environments and Stack Instances", "Site infrastructure (DNS, Traefik, CA)", "unknown resources"},
		Preconditions: []string{"local Host and local Docker daemon", "unchanged configuration, identities and ownership", "exclusive Environment lock", "no symlink or mount boundary escapes"}}
	if r.Options.Downloads {
		p.LossAcknowledgement = target + ":downloads"
	}
	if r.Options.Media {
		if p.LossAcknowledgement == "" {
			p.LossAcknowledgement = target + ":media"
		} else {
			p.LossAcknowledgement += ",media"
		}
	}
	if p.LossAcknowledgement != "" {
		p.LossAcknowledgement += ":permanent-loss"
	}
	resources, missing, err := inventoryDestroy(ctx, plan, environment, r.Options, r.plan.configPath)
	if err != nil {
		return DestroyPlan{}, err
	}
	p.Resources = resources
	p.MissingVolumes = missing
	if err := checkDestroyStorage(declared, r.plan.environment, resources, r.plan.configPath, r.plan.versionsPath); err != nil {
		return DestroyPlan{}, err
	}
	if r.Options.Volumes {
		if err := checkDestroyRecovery(environment); err != nil {
			return DestroyPlan{}, err
		}
		p.Preconditions = append(p.Preconditions, "verified protected final-state backup of surviving owned volumes; no retention")
	}
	if r.Resume != "" {
		if _, err := loadDestroyJournal(r.Resume); err != nil {
			return DestroyPlan{}, err
		}
	}
	return p, nil
}

func destroyEnvironment(r DestroyRequest) (config.Environment, error) {
	d, err := r.plan.load()
	if err != nil {
		return config.Environment{}, err
	}
	if err := d.ValidateEnvironment(r.plan.environment); err != nil {
		return config.Environment{}, err
	}
	return d.Spec.Environments[r.plan.environment], nil
}

func destroyStateDirectory(e config.Environment) string {
	return filepath.Join(e.BackupRoot, ".destroy-operations")
}

func (r DestroyReport) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Destroy target: %s\nHost: %s\nDocker daemon: %s\nExpires: %s\nSHA256: %s\n", r.Plan.Target, r.Plan.Host, r.Plan.Daemon, r.Plan.ExpiresAt.Format(time.RFC3339), r.SHA256)
	for _, resource := range r.Plan.Resources {
		fmt.Fprintf(&b, "Remove %s %s (%s)\n", resource.Kind, resource.Name, resource.Identity)
	}
	for _, name := range r.Plan.MissingVolumes {
		fmt.Fprintf(&b, "Already absent volume: %s\n", name)
	}
	if r.Plan.Options.Volumes {
		surviving := 0
		for _, resource := range r.Plan.Resources {
			if resource.Kind == DestroyVolume {
				surviving++
			}
		}
		fmt.Fprintf(&b, "Application-state coverage: %d surviving selected volumes; %d declared volumes absent. No media or download coverage.\n", surviving, len(r.Plan.MissingVolumes))
		if surviving == 0 {
			b.WriteString("No selected volumes exist; no complete Environment backup will be created.\n")
		}
	}
	fmt.Fprintf(&b, "Retain: %s\nWARNING: %s\n", strings.Join(r.Plan.Retained, "; "), r.Warning)
	if r.Plan.LossAcknowledgement != "" {
		fmt.Fprintf(&b, "Required --acknowledge-loss %q\n", r.Plan.LossAcknowledgement)
	}
	if r.Plan.Environment == "production" {
		fmt.Fprintf(&b, "Required --target %q\n", r.Plan.Target)
	}
	if r.Failure != "" {
		fmt.Fprintf(&b, "Recovery: %s\n", r.Failure)
	}
	if r.JournalPath != "" {
		fmt.Fprintf(&b, "Journal: %s\nBackup: %s\nCompleted: %t\n", r.JournalPath, r.BackupPath, r.Completed)
	}
	return b.String()
}
