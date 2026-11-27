package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const calibreLibraryArchive = "calibre-library.tar"

// Called under the Environment lock while the sole library writer is stopped.
func archiveCalibreLibrary(ctx context.Context, dataRoot, backupDir string) (string, error) {
	library := filepath.Join(dataRoot, "books", "calibre")
	if err := checkDestroyAncestors(library); err != nil {
		return "", err
	}
	catalog, err := os.Lstat(filepath.Join(library, "metadata.db"))
	if err != nil {
		return "", err
	}
	if !catalog.Mode().IsRegular() {
		return "", fmt.Errorf("Calibre catalog must be a regular file")
	}
	path := filepath.Join(backupDir, calibreLibraryArchive)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	cmd := exec.CommandContext(ctx, "tar", "-C", library, "-cf", "-", ".")
	cmd.Stdout = file
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("archive %s: %w", library, err)
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	_, digest, err := checksumFile(path)
	return digest, err
}
