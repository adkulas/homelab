package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollbackRejectsChangedSafetyArchiveBeforeDockerMutation(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "radarr.tar")
	if err := os.WriteFile(archivePath, []byte("changed"), 0o600); err != nil {
		t.Fatalf("write changed safety archive: %v", err)
	}
	source := backupSource{
		serviceName:  "radarr",
		volumeName:   "radarr-config",
		dockerVolume: "media-staging_radarr-config",
		mountPath:    "/config",
		image:        "example.invalid/radarr@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	safety := BackupReport{
		ManifestPath: filepath.Join(root, "manifest.json"),
		Services: []BackupService{{
			Name:           source.serviceName,
			Volume:         source.volumeName,
			DockerVolume:   source.dockerVolume,
			MountPath:      source.mountPath,
			Image:          source.image,
			ArchivePath:    filepath.Base(archivePath),
			SizeBytes:      1,
			ChecksumSHA256: strings.Repeat("0", 64),
		}},
	}

	err := rollbackRestore(context.Background(), "compose.yaml", "media-staging", safety, []backupSource{source})
	if err == nil || !strings.Contains(err.Error(), "verify safety backup checksums") {
		t.Fatalf("rollback safety verification error = %v", err)
	}
}
