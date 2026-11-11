package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

type DestroyAction struct {
	Resource DestroyResource `json:"resource"`
	Status   string          `json:"status"`
}

type destroyHelper struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
}

type destroyJournal struct {
	RunningBeforeStop      []string          `json:"runningBeforeStop,omitempty"`
	StopPrepared           bool              `json:"stopPrepared"`
	PriorJournal           string            `json:"priorJournal,omitempty"`
	Helpers                []destroyHelper   `json:"helpers,omitempty"`
	RecoveryManifestDigest string            `json:"recoveryManifestDigest,omitempty"`
	SchemaVersion          string            `json:"schemaVersion"`
	Plan                   DestroyPlan       `json:"plan"`
	SHA256                 string            `json:"sha256"`
	Backup                 *BackupReport     `json:"backup,omitempty"`
	Ciphertexts            map[string]string `json:"ciphertexts,omitempty"`
	RecoveryDirectory      string            `json:"recoveryDirectory,omitempty"`
	Actions                []DestroyAction   `json:"actions"`
	Stopped                bool              `json:"stopped"`
	DeletionStarted        bool              `json:"deletionStarted"`
	Completed              bool              `json:"completed"`
	Failure                string            `json:"failure,omitempty"`
}

func loadDestroyJournal(path string) (destroyJournal, error) {
	data, err := readPrivateArtifact(path)
	if err != nil {
		return destroyJournal{}, err
	}
	var j destroyJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return j, err
	}
	if j.SchemaVersion != destroySchema || j.Plan.SchemaVersion != destroySchema {
		return j, fmt.Errorf("unsupported destroy journal")
	}
	return j, nil
}
func saveDestroyJournal(path string, j destroyJournal) error {
	if err := writeDestroyState(path, j); err != nil {
		return err
	}
	active := filepath.Join(filepath.Dir(path), "active.json")
	if active == path {
		return nil
	}
	return writeDestroyState(active, j)
}

func (e localEngine) executeDestroy(ctx context.Context, r DestroyRequest) (report DestroyReport, returnErr error) {
	data, err := readPrivateArtifact(r.ArtifactPath)
	if err != nil {
		return report, err
	}
	digest := checksum(data)
	if r.Confirm == "" || digest != r.Confirm {
		return report, fmt.Errorf("plan digest does not match --confirm")
	}
	var p DestroyPlan
	if err := json.Unmarshal(data, &p); err != nil {
		return report, err
	}
	if p.Options.Secrets && !p.Options.Volumes {
		return report, fmt.Errorf("--secrets requires --volumes")
	}
	for _, resource := range p.Resources {
		if resource.Kind.order() < 0 {
			return report, fmt.Errorf("unsupported resource kind in saved plan")
		}
	}
	if p.SchemaVersion != destroySchema || p.Operation != "destroy" || p.Environment != r.plan.environment {
		return report, fmt.Errorf("saved plan operation or Environment does not match")
	}
	if !time.Now().Before(p.ExpiresAt) || p.ExpiresAt.Sub(p.CreatedAt) != 30*time.Minute || p.CreatedAt.After(time.Now().Add(time.Minute)) {
		return report, fmt.Errorf("saved plan authorization expired or has invalid timestamps; preview remaining work again")
	}
	if r.Options != (DestroyOptions{}) && r.Options != p.Options {
		return report, fmt.Errorf("deletion options differ from saved plan")
	}
	if p.Environment == "production" && r.Target != p.Target {
		return report, fmt.Errorf("Production destruction requires --target %q", p.Target)
	}
	if p.LossAcknowledgement != "" && r.AcknowledgeLoss != p.LossAcknowledgement {
		return report, fmt.Errorf("payload deletion requires --acknowledge-loss %q", p.LossAcknowledgement)
	}
	r.Options = p.Options
	r.Resume = p.Resume
	release, err := lockEnvironment(ctx, r.plan, true)
	if err != nil {
		return report, err
	}
	defer release()
	r.plan, err = r.plan.refresh(ctx)
	if err != nil {
		return report, err
	}
	// All volatile preconditions are recomputed under the same lifecycle lock.
	current, err := e.previewDestroy(ctx, r)
	if err != nil {
		return report, err
	}
	if err := compareDestroyPlans(p, current); err != nil {
		return report, err
	}
	if !time.Now().Before(p.ExpiresAt) {
		return report, fmt.Errorf("saved plan authorization expired")
	}
	environment, err := destroyEnvironment(r)
	if err != nil {
		return report, err
	}
	journalPath := filepath.Join(destroyStateDirectory(environment), digest+".json")
	j, err := loadDestroyJournal(journalPath)
	if os.IsNotExist(err) {
		j = destroyJournal{SchemaVersion: destroySchema, Plan: p, SHA256: digest}
		for _, resource := range p.Resources {
			j.Actions = append(j.Actions, DestroyAction{Resource: resource, Status: "pending"})
		}
		activePath := filepath.Join(destroyStateDirectory(environment), "active.json")
		active, activeErr := loadDestroyJournal(activePath)
		if activeErr != nil && !os.IsNotExist(activeErr) {
			return report, activeErr
		}
		if activeErr == nil && !active.Completed {
			if r.Resume == "" {
				return report, fmt.Errorf("unfinished destroy operation; preview with --resume %s", activePath)
			}
			prior, err := loadDestroyJournal(r.Resume)
			if err != nil {
				return report, err
			}

			if prior.SHA256 != active.SHA256 || prior.Plan.Target != p.Target || prior.Plan.Options != p.Options {
				return report, fmt.Errorf("resume journal does not match active operation and target")
			}
			if err := verifyDestroyProtection(prior); err != nil {
				return report, err
			}
			// Keep the prior journal as an immutable recovery link. A renewed preview
			// may authorize replacements, but their final state needs a fresh capture.
			j.PriorJournal = filepath.Join(filepath.Dir(r.Resume), prior.SHA256+".json")
			j.Helpers = prior.Helpers
			j.DeletionStarted = prior.DeletionStarted
			unchanged := compareDestroyPlans(prior.Plan, current) == nil
			for _, resource := range current.Resources {
				if resource.Kind == DestroyContainer && resource.Running && prior.Stopped {
					unchanged = false
				}
			}
			if unchanged {
				j.RecoveryManifestDigest = prior.RecoveryManifestDigest
				j.Backup = prior.Backup
				j.Ciphertexts = prior.Ciphertexts
				j.RecoveryDirectory = prior.RecoveryDirectory
				j.Stopped = prior.Stopped
				j.RunningBeforeStop = prior.RunningBeforeStop
				j.StopPrepared = prior.StopPrepared
			}

		}
	} else if err != nil {
		return report, err
	}
	if j.SHA256 != digest || !reflect.DeepEqual(j.Plan, p) {
		return report, fmt.Errorf("operation journal does not match approved plan")
	}
	report = destroyReport(p, digest)
	report.JournalPath = journalPath
	report.Actions = j.Actions
	if j.Backup != nil {
		report.BackupPath = j.Backup.ManifestPath
	}
	if err := verifyDestroyProtection(j); err != nil {
		return report, err
	}
	if j.Completed {
		if err := saveDestroyJournal(journalPath, j); err != nil {
			return report, err
		}
		report.Completed = true
		report.Actions = j.Actions
		return report, nil
	}
	if err := saveDestroyJournal(journalPath, j); err != nil {
		return report, err
	}
	stopAttempted := false
	defer func() {
		if returnErr != nil && !j.DeletionStarted && stopAttempted {
			recoveryErr := restartDestroyServices(j.RunningBeforeStop)
			returnErr = errors.Join(returnErr, recoveryErr)
			if recoveryErr == nil {
				j.Stopped = false
				j.StopPrepared = false
				j.RunningBeforeStop = nil
			}
			// Restarted writers invalidate any just-published final-state capture.
			j.RecoveryManifestDigest = ""
			j.Backup = nil
			j.Ciphertexts = nil
			j.RecoveryDirectory = ""
		}
		if returnErr != nil {
			j.Failure = "operation interrupted or failed; inspect selected resources and resume using the saved plan while authorized, or preview remaining work with --resume " + journalPath
			if journalErr := saveDestroyJournal(journalPath, j); journalErr != nil {
				returnErr = errors.Join(returnErr, journalErr)
			}
			returnErr = fmt.Errorf("%w; %s", returnErr, j.Failure)
		}
		report.Actions = j.Actions
		report.Failure = j.Failure
		report.Completed = j.Completed
		if j.Backup != nil {
			report.BackupPath = j.Backup.ManifestPath
		}
	}()
	if err := cleanupDestroyHelpers(ctx, &j, journalPath); err != nil {
		return report, err
	}
	if j.Stopped {
		stopAttempted = true
		for _, resource := range current.Resources {
			if resource.Kind == DestroyContainer && resource.Running {
				stopAttempted = false
				return report, fmt.Errorf("a stopped destroy target was restarted; leave services stopped and preview remaining work")
			}
		}
	} else {
		if !j.StopPrepared {
			j.RunningBeforeStop = nil
			for _, resource := range current.Resources {
				if resource.Kind == DestroyContainer && resource.Running {
					j.RunningBeforeStop = append(j.RunningBeforeStop, resource.Name)
				}
			}
			j.StopPrepared = true
			if err := saveDestroyJournal(journalPath, j); err != nil {
				return report, err
			}
		}
		stopAttempted = true
		for _, resource := range current.Resources {
			if resource.Kind == DestroyContainer {
				if _, err := destroyDocker(ctx, "stop", resource.Name); err != nil {
					return report, err
				}
			}
		}
		j.Stopped = true
		if err := saveDestroyJournal(journalPath, j); err != nil {
			return report, err
		}
	}
	if p.Options.Volumes && j.Backup == nil && j.RecoveryDirectory == "" {
		if err := e.protectDestroy(ctx, r, &j, journalPath, environment, current); err != nil {
			return report, fmt.Errorf("final-state protection failed; no deletion attempted: %w", err)
		}
	}
	if err := verifyDestroyProtection(j); err != nil {
		return report, err
	}
	current, err = e.previewDestroy(ctx, r)
	if err != nil {
		return report, err
	}
	if err := compareDestroyPlans(p, current); err != nil {
		return report, err
	}
	for _, resource := range current.Resources {
		if resource.Kind == DestroyContainer && resource.Running {
			return report, fmt.Errorf("a service resumed writing after final-state capture")
		}
	}
	// Persist the irreversible boundary before the first removal. A crash after
	// this point must never trigger an automatic service restart.
	j.DeletionStarted = true
	if err := saveDestroyJournal(journalPath, j); err != nil {
		return report, err
	}
	for index := range j.Actions {
		action := &j.Actions[index]
		if action.Status == "completed" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		action.Status = "pending"
		if err := saveDestroyJournal(journalPath, j); err != nil {
			return report, err
		}
		if err := removeDestroyResource(ctx, action.Resource, current.Resources); err != nil {
			action.Status = "failed"
			return report, err
		}
		action.Status = "completed"
		if err := saveDestroyJournal(journalPath, j); err != nil {
			return report, err
		}
	}
	if p.Options.Volumes {
		if err := retireDestroyRecovery(environment, j.RecoveryDirectory); err != nil {
			return report, err
		}
	}
	j.Completed = true
	j.Failure = ""
	if err := saveDestroyJournal(journalPath, j); err != nil {
		return report, err
	}
	return report, nil
}

func compareDestroyPlans(approved, current DestroyPlan) error {
	if approved.Target != current.Target || approved.Environment != current.Environment || approved.Project != current.Project || approved.Host != current.Host || approved.Daemon != current.Daemon || approved.Endpoint != current.Endpoint || approved.ConfigDigest != current.ConfigDigest || approved.EffectiveConfigDigest != current.EffectiveConfigDigest || approved.VersionDigest != current.VersionDigest || approved.Options != current.Options {
		return fmt.Errorf("saved plan identity, configuration, versions or options changed; create a new preview")
	}
	for _, resource := range current.Resources {
		found := false
		for _, expected := range approved.Resources {
			if expected.Kind != resource.Kind || expected.Name != resource.Name {
				continue
			}
			found = true
			if resource.Identity != "absent" && (resource.Identity != expected.Identity || resource.Owner != expected.Owner) {
				return fmt.Errorf("resource identity or ownership changed: %s %s", resource.Kind, resource.Name)
			}
		}
		if !found {
			return fmt.Errorf("unapproved resource appeared: %s %s", resource.Kind, resource.Name)
		}
	}
	return nil
}
func restartDestroyServices(running []string) error {
	recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result error
	for _, id := range running {
		_, err := destroyDocker(recovery, "start", id)
		result = errors.Join(result, err)
	}
	if result != nil {
		return fmt.Errorf("failed to restore previously running service set: %w", result)
	}
	return nil
}
func removeDestroyResource(ctx context.Context, r DestroyResource, current []DestroyResource) error {
	present := false
	for _, observed := range current {
		if observed.Kind == r.Kind && observed.Name == r.Name && observed.Identity != "absent" {
			present = true
			break
		}
	}
	if !present {
		return nil
	}
	switch r.Kind {
	case DestroyContainer:
		_, err := destroyDocker(ctx, "rm", r.Name)
		return err
	case DestroyNetwork:
		_, err := destroyDocker(ctx, "network", "rm", r.Name)
		return err
	case DestroyVolume:
		data, err := destroyDocker(ctx, "volume", "inspect", r.Name)
		if err != nil {
			return err
		}
		if checksum(data) != r.Identity {
			return fmt.Errorf("volume replaced before removal: %s", r.Name)
		}
		var volumes []destroyVolume
		if err := json.Unmarshal(data, &volumes); err != nil || len(volumes) != 1 {
			return fmt.Errorf("ambiguous volume removal target")
		}
		containers, err := destroyContainers(ctx)
		if err != nil {
			return err
		}
		for _, container := range containers {
			for _, mount := range container.Mounts {
				if mount.Type == "volume" && mount.Name == r.Name || mount.Type == "bind" && destroyOverlaps(mount.Source, volumes[0].Mountpoint) {
					return fmt.Errorf("unexpected consumer appeared before volume removal")
				}
			}
		}
		_, err = destroyDocker(ctx, "volume", "rm", r.Name)
		return err
	case DestroyDrillCredentials:
		return nil // retired with the gate after all selected deletions
	case DestroyRuntimeFile, DestroyDownloads, DestroyMedia, DestroySecrets:
		return removeDestroyTree(r.Name, r.Identity)
	default:
		return fmt.Errorf("unsupported resource kind")
	}
}
