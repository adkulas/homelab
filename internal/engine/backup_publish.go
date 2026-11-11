package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// publishCapturedBackup publishes already captured state. Callers decide coverage,
// quiescing, protection and retention; every backup shares the durability protocol.
func publishCapturedBackup(incomplete, final string, report BackupReport) error {
	if err := verifyBackupArchives(incomplete, report.Services); err != nil {
		return err
	}
	for _, service := range report.Services {
		file, err := os.Open(filepath.Join(incomplete, filepath.FromSlash(service.ArchivePath)))
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	manifest, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}
	manifest = append(manifest, '\n')
	file, err := os.OpenFile(filepath.Join(incomplete, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(manifest); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := syncDirectory(filepath.Join(incomplete, "archives")); err != nil {
		return err
	}
	if err := syncDirectory(incomplete); err != nil {
		return err
	}
	if err := os.Rename(incomplete, final); err != nil {
		return fmt.Errorf("atomically publish backup: %w", err)
	}
	if err := syncDirectory(filepath.Dir(final)); err != nil {
		return err
	}
	if err := verifyPublishedBackup(report); err != nil {
		if quarantineErr := quarantinePublishedBackup(final, incomplete); quarantineErr != nil {
			return errors.Join(err, quarantineErr)
		}
		return err
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
