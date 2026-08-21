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

type restoreOperation struct {
	SchemaVersion      string   `json:"schemaVersion"`
	ID                 string   `json:"id"`
	Status             string   `json:"status"`
	Environment        string   `json:"environment"`
	SourceManifestPath string   `json:"sourceManifestPath"`
	SafetyBackupPath   string   `json:"safetyBackupPath"`
	TemporaryVolumes   []string `json:"temporaryVolumes"`
	ReplacedServices   []string `json:"replacedServices"`
	Failure            string   `json:"failure,omitempty"`
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
		Status:             "preparing",
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
		operation.Status = "failed"
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
		service := restoreService(backup.Services, source.serviceName)
		temporaryVolume := request.plan.environment + "-" + temporaryPrefix + "-" + source.volumeName
		if err := createDockerVolume(ctx, temporaryVolume); err != nil {
			return RestoreReport{}, err
		}
		operation.TemporaryVolumes = append(operation.TemporaryVolumes, temporaryVolume)
		if err := writeRestoreOperation(journalPath, operation); err != nil {
			return RestoreReport{}, err
		}
		archivePath := filepath.Join(filepath.Dir(request.backupPath), filepath.FromSlash(service.ArchivePath))
		if err := restoreAndVerifyDockerVolume(ctx, temporaryVolume, service.Image, archivePath); err != nil {
			return RestoreReport{}, fmt.Errorf("stage %s restore: %w", service.Name, err)
		}
	}

	composePath := filepath.Join(journalDirectory, operationID+"-compose.yaml")
	if err := os.WriteFile(composePath, compose, 0o600); err != nil {
		return RestoreReport{}, fmt.Errorf("write restore Compose plan: %w", err)
	}
	defer os.Remove(composePath)
	serviceNames := make([]string, 0, len(sources))
	for _, source := range sources {
		serviceNames = append(serviceNames, source.serviceName)
	}
	if err := runCompose(ctx, composePath, report.ProjectName, "stop", serviceNames...); err != nil {
		return RestoreReport{}, fmt.Errorf("stop services for restore: %w", err)
	}
	operation.Status = "replacing"
	if err := writeRestoreOperation(journalPath, operation); err != nil {
		return RestoreReport{}, err
	}
	for _, source := range sources {
		service := restoreService(backup.Services, source.serviceName)
		archivePath := filepath.Join(filepath.Dir(request.backupPath), filepath.FromSlash(service.ArchivePath))
		if err := removeDockerVolume(ctx, source.dockerVolume); err != nil {
			return RestoreReport{}, fmt.Errorf("remove existing %s mutable volume: %w", service.Name, err)
		}
		if err := createDockerVolume(ctx, source.dockerVolume); err != nil {
			return RestoreReport{}, fmt.Errorf("create replacement %s mutable volume: %w", service.Name, err)
		}
		if err := restoreAndVerifyDockerVolume(ctx, source.dockerVolume, service.Image, archivePath); err != nil {
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
	operation.Status = "completed"
	if err := writeRestoreOperation(journalPath, operation); err != nil {
		return RestoreReport{}, err
	}
	report.Completed = true
	report.SafetyBackupPath = safety.ManifestPath
	report.OperationJournalPath = journalPath
	return report, nil
}

func restoreService(services []BackupService, name string) BackupService {
	for _, service := range services {
		if service.Name == name {
			return service
		}
	}
	return BackupService{}
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
		_ = removeDockerContainer(context.WithoutCancel(ctx), containerID)
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
		_ = removeDockerContainer(context.WithoutCancel(ctx), containerID)
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
