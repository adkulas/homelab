package engine

import (
	"fmt"
	"os"
	"path/filepath"
)

const dataDirectoryMode = 0o750

var dataLayout = []string{
	"torrents",
	filepath.Join("torrents", "movies"),
	filepath.Join("torrents", "series"),
	"media",
	filepath.Join("media", "movies"),
	filepath.Join("media", "series"),
}

func provisionDataLayout(root string, uid, gid int) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("provision data layout: data root %q must be absolute", root)
	}
	if err := provisionDataRoot(root, uid, gid); err != nil {
		return err
	}

	for _, relative := range dataLayout {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("provision data layout: %q exists and is not a directory", path)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect data directory %q: %w", path, err)
		}
		if err := os.Mkdir(path, dataDirectoryMode); err != nil {
			return fmt.Errorf("create data directory %q: %w", path, err)
		}
		if err := setNewDataDirectoryIdentity(path, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func provisionDataRoot(root string, uid, gid int) error {
	var missing []string
	for candidate := filepath.Clean(root); ; candidate = filepath.Dir(candidate) {
		info, err := os.Stat(candidate)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("provision data root: %q exists and is not a directory", candidate)
			}
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect data root path %q: %w", candidate, err)
		}
		missing = append(missing, candidate)
		if filepath.Dir(candidate) == candidate {
			return fmt.Errorf("provision data root %q: no existing parent directory", root)
		}
	}
	for index := len(missing) - 1; index >= 0; index-- {
		path := missing[index]
		if err := os.Mkdir(path, dataDirectoryMode); err != nil {
			return fmt.Errorf("create data root directory %q: %w", path, err)
		}
		if err := setNewDataDirectoryIdentity(path, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func setNewDataDirectoryIdentity(path string, uid, gid int) error {
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("set data directory %q ownership to %d:%d: %w", path, uid, gid, err)
	}
	if err := os.Chmod(path, dataDirectoryMode); err != nil {
		return fmt.Errorf("set data directory %q permissions: %w", path, err)
	}
	return nil
}
