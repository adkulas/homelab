package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type restoreOperationStatus string

const (
	restorePreparing      restoreOperationStatus = "preparing"
	restoreReplacing      restoreOperationStatus = "replacing"
	restoreRollingBack    restoreOperationStatus = "rolling-back"
	restoreRolledBack     restoreOperationStatus = "rolled-back"
	restoreRollbackFailed restoreOperationStatus = "rollback-failed"
	restoreCompleted      restoreOperationStatus = "completed"
	restoreFailed         restoreOperationStatus = "failed"
)

type restoreOperation struct {
	SchemaVersion      string                 `json:"schemaVersion"`
	ID                 string                 `json:"id"`
	Status             restoreOperationStatus `json:"status"`
	Environment        string                 `json:"environment"`
	SourceManifestPath string                 `json:"sourceManifestPath"`
	SafetyBackupPath   string                 `json:"safetyBackupPath"`
	TemporaryVolumes   []string               `json:"temporaryVolumes"`
	ReplacedServices   []string               `json:"replacedServices"`
	Failure            string                 `json:"failure,omitempty"`
}

func (engine localEngine) executeRestore(ctx context.Context, request RestoreRequest, backup BackupReport, sources []backupSource, compose []byte, report RestoreReport) (result RestoreReport, returnErr error) {
	safety, err := engine.executeBackup(ctx, BackupRequest{
		plan: request.plan, label: "before-restore", protect: true, generatedAt: time.Now().UTC(), skipRetention: true,
	})
	if err != nil {
		return RestoreReport{}, fmt.Errorf("create verified safety backup: %w", err)
	}

	operationID, err := backupID(time.Now().UTC())
	if err != nil {
		return RestoreReport{}, fmt.Errorf("create restore operation ID: %w", err)
	}
	operationID = "restore-" + operationID
	journalDirectory := filepath.Join(filepath.Dir(safety.ManifestPath), "..", ".restore-operations")
	journalDirectory = filepath.Clean(journalDirectory)
	if err := os.MkdirAll(journalDirectory, 0o700); err != nil {
		return RestoreReport{}, fmt.Errorf("create restore operation journal directory: %w", err)
	}
	journalPath := filepath.Join(journalDirectory, operationID+".json")
	operation := restoreOperation{
		SchemaVersion:      "homelab.media-stack/restore-operation/v1alpha1",
		ID:                 operationID,
		Status:             restorePreparing,
		Environment:        request.plan.environment,
		SourceManifestPath: request.backupPath,
		SafetyBackupPath:   safety.ManifestPath,
	}
	if err := writeRestoreOperation(journalPath, operation); err != nil {
		return RestoreReport{}, err
	}
	defer func() {
		if returnErr == nil {
			return
		}
		if operation.Status != restoreRolledBack && operation.Status != restoreRollbackFailed {
			operation.Status = restoreFailed
		}
		operation.Failure = returnErr.Error()
		if journalErr := writeRestoreOperation(journalPath, operation); journalErr != nil {
			returnErr = errors.Join(returnErr, journalErr)
		}
	}()
	defer func() {
		for _, volume := range operation.TemporaryVolumes {
			if cleanupErr := removeDockerVolume(context.WithoutCancel(ctx), volume); cleanupErr != nil {
				if returnErr == nil {
					returnErr = cleanupErr
				} else {
					returnErr = errors.Join(returnErr, cleanupErr)
				}
			}
		}
	}()

	temporaryPrefix := strings.ReplaceAll(operationID, ".", "-")
	for _, source := range sources {
		service, archivePath, err := restoreServiceArchive(filepath.Dir(request.backupPath), backup.Services, source.serviceName)
		if err != nil {
			return RestoreReport{}, err
		}
		temporaryVolume := request.plan.environment + "-" + temporaryPrefix + "-" + source.volumeName
		if err := createDockerVolume(ctx, temporaryVolume); err != nil {
			return RestoreReport{}, err
		}
		operation.TemporaryVolumes = append(operation.TemporaryVolumes, temporaryVolume)
		if err := writeRestoreOperation(journalPath, operation); err != nil {
			return RestoreReport{}, err
		}
		if err := restoreAndVerifyDockerVolume(ctx, temporaryVolume, service.Image, archivePath); err != nil {
			return RestoreReport{}, fmt.Errorf("stage %s restore: %w", service.Name, err)
		}
	}

	composePath := filepath.Join(journalDirectory, operationID+"-compose.yaml")
	if err := os.WriteFile(composePath, compose, 0o600); err != nil {
		return RestoreReport{}, fmt.Errorf("write restore Compose plan: %w", err)
	}
	defer os.Remove(composePath)
	if err := runCompose(ctx, composePath, report.ProjectName, "down"); err != nil {
		return RestoreReport{}, fmt.Errorf("remove service containers for restore: %w", err)
	}
	stackDown := true
	defer func() {
		if returnErr == nil || !stackDown {
			return
		}
		originalError := returnErr
		operation.Status = restoreRollingBack
		operation.Failure = originalError.Error()
		if journalErr := writeRestoreOperation(journalPath, operation); journalErr != nil {
			returnErr = errors.Join(returnErr, journalErr)
		}
		rollbackErr := rollbackRestore(context.WithoutCancel(ctx), composePath, report.ProjectName, safety, sources)
		if rollbackErr == nil {
			operation.Status = restoreRolledBack
		} else {
			operation.Status = restoreRollbackFailed
			returnErr = errors.Join(returnErr, rollbackErr)
		}
		if journalErr := writeRestoreOperation(journalPath, operation); journalErr != nil {
			returnErr = errors.Join(returnErr, journalErr)
		}
	}()
	operation.Status = restoreReplacing
	if err := writeRestoreOperation(journalPath, operation); err != nil {
		return RestoreReport{}, err
	}
	for index, source := range sources {
		service, _, err := restoreServiceArchive(filepath.Dir(request.backupPath), backup.Services, source.serviceName)
		if err != nil {
			return RestoreReport{}, err
		}
		if err := replaceDockerVolumeFromVolume(ctx, operation.TemporaryVolumes[index], source.dockerVolume, service.Image); err != nil {
			return RestoreReport{}, fmt.Errorf("replace %s mutable volume: %w", service.Name, err)
		}
		operation.ReplacedServices = append(operation.ReplacedServices, service.Name)
		if err := writeRestoreOperation(journalPath, operation); err != nil {
			return RestoreReport{}, err
		}
	}
	if err := runCompose(ctx, composePath, report.ProjectName, "up", "-d"); err != nil {
		return RestoreReport{}, fmt.Errorf("start restored services in dependency order: %w", err)
	}
	stackDown = false
	operation.Status = restoreCompleted
	if err := writeRestoreOperation(journalPath, operation); err != nil {
		return RestoreReport{}, err
	}
	report.Completed = true
	report.SafetyBackupPath = safety.ManifestPath
	report.OperationJournalPath = journalPath
	return report, nil
}

func restoreServiceArchive(root string, services []BackupService, name string) (BackupService, string, error) {
	for _, service := range services {
		if service.Name == name {
			return service, filepath.Join(root, filepath.FromSlash(service.ArchivePath)), nil
		}
	}
	return BackupService{}, "", fmt.Errorf("backup does not contain service %q", name)
}

func replaceDockerVolumeFromVolume(ctx context.Context, sourceVolume, targetVolume, image string) error {
	if err := removeDockerVolume(ctx, targetVolume); err != nil {
		return err
	}
	if err := createDockerVolume(ctx, targetVolume); err != nil {
		return err
	}
	return copyAndVerifyDockerVolume(ctx, sourceVolume, targetVolume, image)
}

func copyAndVerifyDockerVolume(ctx context.Context, sourceVolume, targetVolume, image string) error {
	expected, err := dockerVolumeContentDigest(ctx, sourceVolume, image)
	if err != nil {
		return fmt.Errorf("digest staged volume: %w", err)
	}
	sourceContainer, err := createDockerVolumeContainer(ctx, sourceVolume, "/source:ro", image)
	if err != nil {
		return err
	}
	defer func() {
		if sourceContainer != "" {
			_ = removeDockerContainer(context.WithoutCancel(ctx), sourceContainer)
		}
	}()
	targetContainer, err := createDockerVolumeContainer(ctx, targetVolume, "/target", image)
	if err != nil {
		return err
	}
	defer func() {
		if targetContainer != "" {
			_ = removeDockerContainer(context.WithoutCancel(ctx), targetContainer)
		}
	}()

	sourceCommand := exec.CommandContext(ctx, "docker", "cp", sourceContainer+":/source/.", "-")
	stream, err := sourceCommand.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stream staged volume: %w", err)
	}
	var sourceStderr bytes.Buffer
	sourceCommand.Stderr = &sourceStderr
	targetCommand := exec.CommandContext(ctx, "docker", "cp", "-", targetContainer+":/target")
	targetCommand.Stdin = stream
	var targetStderr bytes.Buffer
	targetCommand.Stderr = &targetStderr
	if err := sourceCommand.Start(); err != nil {
		return fmt.Errorf("start staged-volume stream: %w", err)
	}
	if err := targetCommand.Start(); err != nil {
		_ = sourceCommand.Process.Kill()
		_ = sourceCommand.Wait()
		return fmt.Errorf("start replacement-volume copy: %w", err)
	}
	sourceErr := sourceCommand.Wait()
	targetErr := targetCommand.Wait()
	if sourceErr != nil {
		return fmt.Errorf("read staged volume: %w: %s", sourceErr, bytes.TrimSpace(sourceStderr.Bytes()))
	}
	if targetErr != nil {
		return fmt.Errorf("write replacement volume: %w: %s", targetErr, bytes.TrimSpace(targetStderr.Bytes()))
	}
	if err := removeDockerContainer(context.WithoutCancel(ctx), targetContainer); err != nil {
		return err
	}
	targetContainer = ""
	if err := removeDockerContainer(context.WithoutCancel(ctx), sourceContainer); err != nil {
		return err
	}
	sourceContainer = ""
	actual, err := dockerVolumeContentDigest(ctx, targetVolume, image)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("replacement volume content verification failed")
	}
	return nil
}

func rollbackRestore(ctx context.Context, composePath, projectName string, safety BackupReport, sources []backupSource) error {
	var rollbackErr error
	root := filepath.Dir(safety.ManifestPath)
	for _, source := range sources {
		service, archivePath, err := restoreServiceArchive(root, safety.Services, source.serviceName)
		if err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if err := removeDockerVolume(ctx, source.dockerVolume); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove partial %s volume during rollback: %w", service.Name, err))
			continue
		}
		if err := createDockerVolume(ctx, source.dockerVolume); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("recreate %s volume during rollback: %w", service.Name, err))
			continue
		}
		if err := restoreAndVerifyDockerVolume(ctx, source.dockerVolume, service.Image, archivePath); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s safety archive: %w", service.Name, err))
		}
	}
	if err := runCompose(ctx, composePath, projectName, "up", "-d"); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restart services after rollback: %w", err))
	}
	return rollbackErr
}

func createDockerVolumeContainer(ctx context.Context, volume, mount, image string) (string, error) {
	output, err := exec.CommandContext(ctx, "docker", "create", "--volume", volume+":"+mount, "--entrypoint", "/bin/true", image).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create Docker volume helper: %w: %s", err, bytes.TrimSpace(output))
	}
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return "", fmt.Errorf("create Docker volume helper: Docker returned an empty container ID")
	}
	return containerID, nil
}

func createDockerVolume(ctx context.Context, name string) error {
	output, err := exec.CommandContext(ctx, "docker", "volume", "create", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker volume create %q: %w: %s", name, err, bytes.TrimSpace(output))
	}
	return nil
}

func removeDockerVolume(ctx context.Context, name string) error {
	output, err := exec.CommandContext(ctx, "docker", "volume", "rm", "--force", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker volume rm %q: %w: %s", name, err, bytes.TrimSpace(output))
	}
	return nil
}

func restoreAndVerifyDockerVolume(ctx context.Context, volume, image, archivePath string) error {
	expected, err := archiveContentDigestFromFile(archivePath)
	if err != nil {
		return err
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open restore archive: %w", err)
	}
	create := exec.CommandContext(ctx, "docker", "create", "--volume", volume+":/target", "--entrypoint", "/bin/true", image)
	output, err := create.CombinedOutput()
	if err != nil {
		archive.Close()
		return fmt.Errorf("create restore helper: %w: %s", err, bytes.TrimSpace(output))
	}
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		archive.Close()
		return fmt.Errorf("create restore helper: Docker returned an empty container ID")
	}
	defer func() {
		if containerID != "" {
			_ = removeDockerContainer(context.WithoutCancel(ctx), containerID)
		}
	}()
	command := exec.CommandContext(ctx, "docker", "cp", "-", containerID+":/target")
	command.Stdin = archive
	var stderr bytes.Buffer
	command.Stderr = &stderr
	runErr := command.Run()
	closeErr := archive.Close()
	if runErr != nil {
		return fmt.Errorf("extract restore archive: %w: %s", runErr, bytes.TrimSpace(stderr.Bytes()))
	}
	if closeErr != nil {
		return fmt.Errorf("close restore archive: %w", closeErr)
	}
	if err := removeDockerContainer(context.WithoutCancel(ctx), containerID); err != nil {
		return err
	}
	containerID = ""
	actual, err := dockerVolumeContentDigest(ctx, volume, image)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("restored content verification failed")
	}
	return nil
}

func dockerVolumeContentDigest(ctx context.Context, volume, image string) (string, error) {
	create := exec.CommandContext(ctx, "docker", "create", "--volume", volume+":/source:ro", "--entrypoint", "/bin/true", image)
	output, err := create.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create restored-volume verification helper: %w: %s", err, bytes.TrimSpace(output))
	}
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return "", fmt.Errorf("create restored-volume verification helper: Docker returned an empty container ID")
	}
	defer func() {
		if containerID != "" {
			_ = removeDockerContainer(context.WithoutCancel(ctx), containerID)
		}
	}()
	command := exec.CommandContext(ctx, "docker", "cp", containerID+":/source/.", "-")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("capture restored volume: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start restored-volume verification: %w", err)
	}
	digest, digestErr := archiveContentDigest(stdout)
	waitErr := command.Wait()
	if digestErr != nil {
		return "", digestErr
	}
	if waitErr != nil {
		return "", fmt.Errorf("archive restored volume: %w: %s", waitErr, bytes.TrimSpace(stderr.Bytes()))
	}
	if err := removeDockerContainer(context.WithoutCancel(ctx), containerID); err != nil {
		return "", err
	}
	containerID = ""
	return digest, nil
}

func removeDockerContainer(ctx context.Context, containerID string) error {
	output, err := exec.CommandContext(ctx, "docker", "rm", "--force", containerID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove restore helper %q: %w: %s", containerID, err, bytes.TrimSpace(output))
	}
	return nil
}

func archiveContentDigestFromFile(path string) (string, error) {
	archive, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open restore archive: %w", err)
	}
	defer archive.Close()
	return archiveContentDigest(archive)
}

func archiveContentDigest(reader io.Reader) (string, error) {
	archive := tar.NewReader(reader)
	var records []string
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read restore archive: %w", err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(header.Name)), "./")
		if name == "." || name == "" {
			continue
		}
		if filepath.IsAbs(header.Name) || name == ".." || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("restore archive contains unsafe path %q", header.Name)
		}
		fileDigest := ""
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			hash := sha256.New()
			if _, err := io.Copy(hash, archive); err != nil {
				return "", fmt.Errorf("checksum restored file %q: %w", name, err)
			}
			fileDigest = hex.EncodeToString(hash.Sum(nil))
		}
		records = append(records, fmt.Sprintf("%s|%d|%o|%d|%d|%d|%s|%s", name, header.Typeflag, header.Mode, header.Uid, header.Gid, header.Size, header.Linkname, fileDigest))
	}
	sort.Strings(records)
	hash := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return hex.EncodeToString(hash[:]), nil
}

func writeRestoreOperation(path string, operation restoreOperation) error {
	contents, err := json.MarshalIndent(operation, "", "  ")
	if err != nil {
		return fmt.Errorf("encode restore operation journal: %w", err)
	}
	contents = append(contents, '\n')
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		return fmt.Errorf("write restore operation journal: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish restore operation journal: %w", err)
	}
	return nil
}
