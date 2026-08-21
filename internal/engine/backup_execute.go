package engine

import (
	"bytes"
	"context"
	"crypto/rand"
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

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

const backupCLIContractVersion = "homelab.media-stack/cli/v1alpha1"

type renderedBackupProject struct {
	Services map[string]renderedBackupService
	Volumes  map[string]any
}

type renderedBackupService struct {
	Image   string
	Volumes []string
}

type backupSource struct {
	serviceName  string
	volumeName   string
	dockerVolume string
	mountPath    string
	image        string
}

func (engine localEngine) executeBackup(ctx context.Context, request BackupRequest) (report BackupReport, returnErr error) {
	if err := ctx.Err(); err != nil {
		return BackupReport{}, err
	}
	declared, err := config.Load(request.plan.configPath)
	if err != nil {
		return BackupReport{}, err
	}
	if err := declared.ValidateBackupEnvironment(request.plan.environment); err != nil {
		return BackupReport{}, err
	}
	environment := declared.Spec.Environments[request.plan.environment]
	configContents, err := os.ReadFile(request.plan.configPath)
	if err != nil {
		return BackupReport{}, fmt.Errorf("read Declared Configuration: %w", err)
	}
	versionContents, err := os.ReadFile(request.plan.versionsPath)
	if err != nil {
		return BackupReport{}, fmt.Errorf("read checked-in versions: %w", err)
	}
	plan, err := engine.Plan(ctx, request.plan)
	if err != nil {
		return BackupReport{}, fmt.Errorf("plan backup coverage: %w", err)
	}
	sources, err := backupSources(plan.Compose(), environment.ProjectName)
	if err != nil {
		return BackupReport{}, err
	}

	generatedAt := time.Now().UTC()
	id, err := backupID(generatedAt)
	if err != nil {
		return BackupReport{}, err
	}
	if err := os.MkdirAll(environment.BackupRoot, 0o700); err != nil {
		return BackupReport{}, fmt.Errorf("create %s Environment backup root: %w", request.plan.environment, err)
	}
	incompletePath := filepath.Join(environment.BackupRoot, ".incomplete-"+id)
	finalPath := filepath.Join(environment.BackupRoot, id)
	if err := os.Mkdir(incompletePath, 0o700); err != nil {
		return BackupReport{}, fmt.Errorf("create incomplete backup: %w", err)
	}
	archiveDirectory := filepath.Join(incompletePath, "archives")
	if err := os.Mkdir(archiveDirectory, 0o700); err != nil {
		return BackupReport{}, fmt.Errorf("create backup archive directory: %w", err)
	}
	composePath := filepath.Join(incompletePath, ".compose.yaml")
	if err := os.WriteFile(composePath, plan.Compose(), 0o600); err != nil {
		return BackupReport{}, fmt.Errorf("write backup Compose plan: %w", err)
	}

	running, err := runningBackupServices(ctx, composePath, environment.ProjectName, sources)
	if err != nil {
		return BackupReport{}, err
	}
	runningNames := sortedSet(running)
	resumePending := false
	if len(runningNames) > 0 {
		if err := runCompose(ctx, composePath, environment.ProjectName, "stop", runningNames...); err != nil {
			return BackupReport{}, fmt.Errorf("quiesce mutable services: %w", err)
		}
		resumePending = true
		defer func() {
			if !resumePending {
				return
			}
			resumeError := runCompose(context.WithoutCancel(ctx), composePath, environment.ProjectName, "start", runningNames...)
			if resumeError != nil {
				resumeError = fmt.Errorf("resume quiesced services: %w", resumeError)
				if returnErr == nil {
					returnErr = resumeError
				} else {
					returnErr = errors.Join(returnErr, resumeError)
				}
			}
		}()
	}

	services := make([]BackupService, 0, len(sources))
	for _, source := range sources {
		relativeArchivePath := filepath.ToSlash(filepath.Join("archives", source.serviceName+".tar"))
		archivePath := filepath.Join(incompletePath, filepath.FromSlash(relativeArchivePath))
		size, digest, err := archiveDockerVolume(ctx, source, archivePath)
		if err != nil {
			return BackupReport{}, err
		}
		method := "read-only-volume-archive"
		if running[source.serviceName] {
			method = "compose-stop+" + method
		}
		services = append(services, BackupService{
			Name:              source.serviceName,
			Volume:            source.volumeName,
			DockerVolume:      source.dockerVolume,
			MountPath:         source.mountPath,
			Image:             source.image,
			ArchivePath:       relativeArchivePath,
			ChecksumSHA256:    digest,
			ConsistencyMethod: method,
			SizeBytes:         size,
		})
	}

	if resumePending {
		if err := runCompose(context.WithoutCancel(ctx), composePath, environment.ProjectName, "start", runningNames...); err != nil {
			return BackupReport{}, fmt.Errorf("resume quiesced services: %w", err)
		}
		resumePending = false
	}
	if err := os.Remove(composePath); err != nil {
		return BackupReport{}, fmt.Errorf("remove temporary backup Compose plan: %w", err)
	}

	report = BackupReport{
		SchemaVersion:      backupSchemaVersion,
		CLIContractVersion: backupCLIContractVersion,
		ID:                 id,
		ManifestPath:       filepath.Join(finalPath, "manifest.json"),
		Environment:        request.plan.environment,
		ProjectName:        environment.ProjectName,
		Label:              request.label,
		Protected:          request.protect,
		ConfigDigest:       checksum(configContents),
		VersionDigest:      checksum(versionContents),
		GeneratedAt:        generatedAt,
		Complete:           true,
		Services:           services,
	}
	if err := verifyBackupArchives(incompletePath, report.Services); err != nil {
		return BackupReport{}, err
	}
	manifest, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return BackupReport{}, fmt.Errorf("encode backup manifest: %w", err)
	}
	manifest = append(manifest, '\n')
	manifestPath := filepath.Join(incompletePath, "manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		return BackupReport{}, fmt.Errorf("write backup manifest: %w", err)
	}
	if err := os.Rename(incompletePath, finalPath); err != nil {
		return BackupReport{}, fmt.Errorf("atomically publish backup: %w", err)
	}
	if err := verifyPublishedBackup(report); err != nil {
		return BackupReport{}, err
	}
	return report, nil
}

func backupSources(compose []byte, projectName string) ([]backupSource, error) {
	var project renderedBackupProject
	if err := yaml.Unmarshal(compose, &project); err != nil {
		return nil, fmt.Errorf("decode rendered Compose for backup: %w", err)
	}
	seen := make(map[string]bool, len(project.Volumes))
	sources := make([]backupSource, 0, len(project.Volumes))
	for serviceName, service := range project.Services {
		for _, mount := range service.Volumes {
			parts := strings.Split(mount, ":")
			if len(parts) < 2 {
				continue
			}
			volumeName, mountPath := parts[0], parts[1]
			if _, declared := project.Volumes[volumeName]; !declared {
				continue
			}
			if seen[volumeName] {
				return nil, fmt.Errorf("mutable volume %q is mounted by more than one service", volumeName)
			}
			seen[volumeName] = true
			sources = append(sources, backupSource{
				serviceName:  serviceName,
				volumeName:   volumeName,
				dockerVolume: projectName + "_" + volumeName,
				mountPath:    mountPath,
				image:        service.Image,
			})
		}
	}
	for volumeName := range project.Volumes {
		if !seen[volumeName] {
			return nil, fmt.Errorf("rendered mutable volume %q has no service owner", volumeName)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].serviceName < sources[j].serviceName })
	return sources, nil
}

func runningBackupServices(ctx context.Context, composePath, projectName string, sources []backupSource) (map[string]bool, error) {
	command := composeCommand(ctx, composePath, projectName, "ps", "--status", "running", "--services")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("observe running services: %w: %s", err, bytes.TrimSpace(output))
	}
	covered := make(map[string]bool, len(sources))
	for _, source := range sources {
		covered[source.serviceName] = true
	}
	running := make(map[string]bool)
	for _, serviceName := range strings.Fields(string(output)) {
		if covered[serviceName] {
			running[serviceName] = true
		}
	}
	return running, nil
}

func sortedSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func runCompose(ctx context.Context, composePath, projectName, operation string, serviceNames ...string) error {
	arguments := append([]string{operation}, serviceNames...)
	command := composeCommand(ctx, composePath, projectName, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose %s: %w: %s", operation, err, bytes.TrimSpace(output))
	}
	return nil
}

func composeCommand(ctx context.Context, composePath, projectName string, arguments ...string) *exec.Cmd {
	base := []string{"compose", "--project-name", projectName, "--file", composePath}
	return exec.CommandContext(ctx, "docker", append(base, arguments...)...)
}

func archiveDockerVolume(ctx context.Context, source backupSource, archivePath string) (size int64, digest string, returnErr error) {
	create := exec.CommandContext(ctx, "docker", "create", "--volume", source.dockerVolume+":/source:ro", "--entrypoint", "/bin/true", source.image)
	output, err := create.CombinedOutput()
	if err != nil {
		return 0, "", fmt.Errorf("create read-only archive helper for %s: %w: %s", source.serviceName, err, bytes.TrimSpace(output))
	}
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return 0, "", fmt.Errorf("create read-only archive helper for %s: Docker returned an empty container ID", source.serviceName)
	}
	defer func() {
		if containerID == "" {
			return
		}
		remove := exec.CommandContext(context.WithoutCancel(ctx), "docker", "rm", "--force", containerID)
		if output, err := remove.CombinedOutput(); err != nil {
			cleanupError := fmt.Errorf("remove archive helper for %s: %w: %s", source.serviceName, err, bytes.TrimSpace(output))
			if returnErr == nil {
				returnErr = cleanupError
			} else {
				returnErr = errors.Join(returnErr, cleanupError)
			}
		}
	}()

	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("create %s archive: %w", source.serviceName, err)
	}
	copyCommand := exec.CommandContext(ctx, "docker", "cp", containerID+":/source/.", "-")
	var stderr bytes.Buffer
	copyCommand.Stdout = archive
	copyCommand.Stderr = &stderr
	copyError := copyCommand.Run()
	closeError := archive.Close()
	if copyError != nil {
		return 0, "", fmt.Errorf("archive %s mutable volume: %w: %s", source.serviceName, copyError, bytes.TrimSpace(stderr.Bytes()))
	}
	if closeError != nil {
		return 0, "", fmt.Errorf("close %s archive: %w", source.serviceName, closeError)
	}
	if output, err := exec.CommandContext(context.WithoutCancel(ctx), "docker", "rm", "--force", containerID).CombinedOutput(); err != nil {
		return 0, "", fmt.Errorf("remove archive helper for %s: %w: %s", source.serviceName, err, bytes.TrimSpace(output))
	}
	containerID = ""
	return checksumFile(archivePath)
}

func checksumFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", fmt.Errorf("open archived volume %q: %w", path, err)
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return 0, "", fmt.Errorf("checksum archived volume %q: %w", path, err)
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

func verifyBackupArchives(root string, services []BackupService) error {
	for _, service := range services {
		path := filepath.Join(root, filepath.FromSlash(service.ArchivePath))
		size, digest, err := checksumFile(path)
		if err != nil {
			return err
		}
		if size != service.SizeBytes || digest != service.ChecksumSHA256 {
			return fmt.Errorf("verify %s archive: checksum or size changed after write", service.Name)
		}
	}
	return nil
}

func verifyPublishedBackup(report BackupReport) error {
	manifest, err := os.ReadFile(report.ManifestPath)
	if err != nil {
		return fmt.Errorf("verify published manifest: %w", err)
	}
	if !json.Valid(manifest) {
		return fmt.Errorf("verify published manifest: invalid JSON")
	}
	return verifyBackupArchives(filepath.Dir(report.ManifestPath), report.Services)
}

func backupID(generatedAt time.Time) (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate backup ID: %w", err)
	}
	return generatedAt.Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random[:]), nil
}
