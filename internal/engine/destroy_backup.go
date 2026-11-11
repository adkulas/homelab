package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/config"
)

func (e localEngine) protectDestroy(ctx context.Context, r DestroyRequest, j *destroyJournal, journalPath string, environment config.Environment, current DestroyPlan) error {
	plan, err := e.Plan(ctx, r.plan)
	if err != nil {
		return err
	}
	sources, err := backupSources(plan.Compose(), environment.ProjectName)
	if err != nil {
		return err
	}
	id, err := backupID(time.Now().UTC())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(environment.BackupRoot, 0700); err != nil {
		return err
	}
	incomplete := filepath.Join(environment.BackupRoot, ".incomplete-"+id)
	final := filepath.Join(environment.BackupRoot, id)
	if err := os.Mkdir(incomplete, 0700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(incomplete, "archives"), 0700); err != nil {
		return err
	}
	report := BackupReport{Scope: environment.SecretScope, SchemaVersion: backupSchemaVersion, CLIContractVersion: backupCLIContractVersion, ID: id, ManifestPath: filepath.Join(final, "manifest.json"), Environment: r.plan.environment, ProjectName: environment.ProjectName, Label: "before-destroy", Protected: true, ConfigDigest: j.Plan.ConfigDigest, VersionDigest: j.Plan.VersionDigest, GeneratedAt: time.Now().UTC(), MissingVolumes: append([]string(nil), current.MissingVolumes...)}
	selected := map[string]DestroyResource{}
	for _, resource := range current.Resources {
		if resource.Kind == DestroyVolume {
			selected[resource.Name] = resource
		}
	}
	for _, source := range sources {
		resource, exists := selected[source.dockerVolume]
		if !exists {
			continue
		}
		relative := filepath.ToSlash(filepath.Join("archives", source.serviceName+".tar"))
		size, digest, err := archiveDestroyVolume(ctx, source, resource, filepath.Join(incomplete, filepath.FromSlash(relative)), j, journalPath)
		if err != nil {
			return fmt.Errorf("cannot independently protect %s volume: %w", source.serviceName, err)
		}
		report.Services = append(report.Services, BackupService{Name: source.serviceName, Volume: source.volumeName, DockerVolume: source.dockerVolume, MountPath: source.mountPath, Image: source.image, ArchivePath: relative, ChecksumSHA256: digest, SizeBytes: size, ConsistencyMethod: quiescedReadOnlyVolumeArchive})
	}
	report.Complete = len(report.Services) == len(sources) && len(sources) > 0 && len(report.MissingVolumes) == 0
	ciphertexts := map[string]string{}
	for _, resource := range j.Plan.Resources {
		if (resource.Kind != DestroySecrets && resource.Kind != DestroyDrillCredentials) || resource.Identity == "absent" {
			continue
		}
		identity, err := destroyPathIdentity(resource.Name, false)
		if err != nil {
			return err
		}
		if identity != resource.Identity {
			return fmt.Errorf("recovery ciphertext changed since approval")
		}
		contents, err := os.ReadFile(resource.Name)
		if err != nil {
			return err
		}
		name := string(resource.Kind) + ".sops.yaml"
		if err := createPrivateArtifact(filepath.Join(incomplete, name), contents); err != nil {
			return fmt.Errorf("preserve recovery ciphertext: %w", err)
		}
		ciphertexts[name] = checksum(contents)
		report.RecoveryDocuments = append(report.RecoveryDocuments, name)
	}
	if err := publishCapturedBackup(incomplete, final, report); err != nil {
		return err
	}
	j.RecoveryDirectory = final
	manifest, err := readPrivateArtifact(report.ManifestPath)
	if err != nil {
		return err
	}
	j.RecoveryManifestDigest = checksum(manifest)
	j.Ciphertexts = ciphertexts
	// An empty installation has a recovery record but no invented Environment backup.
	if len(report.Services) > 0 {
		j.Backup = &report
	}
	if err := verifyDestroyProtection(*j); err != nil {
		return err
	}
	return saveDestroyJournal(journalPath, *j)
}

// Bind the existing local volume directory rather than asking Docker to mount a
// named volume: --volume would silently create a missing volume. --mount bind
// fails when the original storage is absent. Helpers never start services.
func archiveDestroyVolume(ctx context.Context, source backupSource, resource DestroyResource, path string, j *destroyJournal, journalPath string) (int64, string, error) {
	data, err := destroyDocker(ctx, "volume", "inspect", source.dockerVolume)
	if err != nil {
		return 0, "", err
	}
	if checksum(data) != resource.Identity {
		return 0, "", fmt.Errorf("volume identity changed before capture")
	}
	var volumes []destroyVolume
	if err := json.Unmarshal(data, &volumes); err != nil || len(volumes) != 1 || volumes[0].Mountpoint == "" {
		return 0, "", fmt.Errorf("cannot bind existing volume storage")
	}
	output, err := destroyDocker(ctx, "create", "--label", "com.homelab.destroy.operation="+j.SHA256, "--mount", "type=bind,source="+volumes[0].Mountpoint+",target=/source,readonly", "--entrypoint", "/bin/true", source.image)
	if err != nil {
		return 0, "", err
	}
	container := strings.TrimSpace(string(output))
	if container == "" {
		return 0, "", fmt.Errorf("archive helper identity missing")
	}
	defer func() {
		if container != "" {
			destroyDocker(context.WithoutCancel(ctx), "rm", "--force", "--volumes", container)
		}
	}()
	j.Helpers = append(j.Helpers, destroyHelper{ID: container, Operation: j.SHA256})
	if err := saveDestroyJournal(journalPath, *j); err != nil {
		return 0, "", err
	}

	archive, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, "", err
	}
	command := exec.CommandContext(ctx, "docker", "cp", "--archive", container+":/source/.", "-")
	command.Stdout = archive
	if err := command.Run(); err != nil {
		archive.Close()
		return 0, "", fmt.Errorf("capture volume archive failed")
	}
	syncErr := archive.Sync()
	closeErr := archive.Close()
	if syncErr != nil {
		return 0, "", syncErr
	}
	if closeErr != nil {
		return 0, "", closeErr
	}
	expected, err := archiveContentDigestFromFile(path)
	if err != nil {
		return 0, "", fmt.Errorf("invalid captured archive")
	}
	// A second independent stream verifies archived content against the still
	// quiesced volume, rather than trusting a checksum of the first stream alone.
	var observed bytes.Buffer
	command = exec.CommandContext(ctx, "docker", "cp", "--archive", container+":/source/.", "-")
	reader, err := command.StdoutPipe()
	if err != nil {
		return 0, "", err
	}
	command.Stderr = &observed
	if err := command.Start(); err != nil {
		return 0, "", fmt.Errorf("start independent volume verification failed")
	}
	actual, digestErr := archiveContentDigest(reader)
	if digestErr != nil {
		reader.Close()
	}
	waitErr := command.Wait()
	if digestErr != nil || waitErr != nil || actual != expected {
		return 0, "", fmt.Errorf("independent final-state volume verification failed")
	}
	data, err = destroyDocker(ctx, "volume", "inspect", source.dockerVolume)
	if err != nil || checksum(data) != resource.Identity {
		return 0, "", fmt.Errorf("volume identity changed during protection")
	}
	if _, err := destroyDocker(ctx, "rm", "--force", "--volumes", container); err != nil {
		return 0, "", err
	}
	j.Helpers = j.Helpers[:len(j.Helpers)-1]
	if err := saveDestroyJournal(journalPath, *j); err != nil {
		return 0, "", err
	}
	container = ""
	return checksumFile(path)
}

func verifyDestroyProtection(j destroyJournal) error {
	return verifyDestroyProtectionChain(j, map[string]bool{})
}
func verifyDestroyProtectionChain(j destroyJournal, seen map[string]bool) error {
	if seen[j.SHA256] {
		return fmt.Errorf("cyclic destroy recovery journal")
	}
	seen[j.SHA256] = true
	if j.PriorJournal != "" {
		prior, err := loadDestroyJournal(j.PriorJournal)
		if err != nil {
			return err
		}
		if err := verifyDestroyProtectionChain(prior, seen); err != nil {
			return err
		}
	}

	if j.RecoveryDirectory != "" {
		data, err := readPrivateArtifact(filepath.Join(j.RecoveryDirectory, "manifest.json"))
		if err != nil {
			return err
		}
		if checksum(data) != j.RecoveryManifestDigest {
			return fmt.Errorf("protected recovery manifest changed")
		}
	}
	if j.Backup != nil {
		if !j.Backup.Protected || j.Backup.Label != "before-destroy" || j.Backup.Environment != j.Plan.Environment || j.Backup.ProjectName != j.Plan.Project {
			return fmt.Errorf("destroy backup ownership or protection changed")
		}
		if err := verifyPublishedBackup(*j.Backup); err != nil {
			return err
		}
	}
	for name, digest := range j.Ciphertexts {
		if filepath.Base(name) != name || j.RecoveryDirectory == "" {
			return fmt.Errorf("invalid ciphertext recovery location")
		}
		path := filepath.Join(j.RecoveryDirectory, name)
		data, err := readPrivateArtifact(path)
		if err != nil {
			return err
		}
		if checksum(data) != digest {
			return fmt.Errorf("protected recovery ciphertext changed")
		}
	}
	return nil
}
func retireDestroyRecovery(environment config.Environment, directory string) error {
	if directory == "" {
		return fmt.Errorf("missing protected recovery record")
	}
	for _, name := range []string{".restore-operations", ".restore-drill"} {
		source := filepath.Join(environment.BackupRoot, name)
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := checkDestroyAncestors(source); err != nil {
			return err
		}
		destination := filepath.Join(directory, "retired"+name)
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("recovery retirement destination already exists")
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
		if err := syncDirectory(directory); err != nil {
			return err
		}
		if err := syncDirectory(environment.BackupRoot); err != nil {
			return err
		}
	}
	return nil
}

func cleanupDestroyHelpers(ctx context.Context, j *destroyJournal, path string) error {
	if len(j.Helpers) == 0 {
		return nil
	}
	containers, err := destroyContainers(ctx)
	if err != nil {
		return err
	}
	for _, id := range j.Helpers {
		for _, c := range containers {
			if c.ID == id.ID {
				if c.State.Running || c.Config.Labels["com.homelab.destroy.operation"] != id.Operation {
					return fmt.Errorf("archive helper identity or lifecycle changed")
				}
				if _, err := destroyDocker(ctx, "rm", "--force", "--volumes", id.ID); err != nil {
					return err
				}
			}
		}
	}
	j.Helpers = nil
	return saveDestroyJournal(path, *j)
}
