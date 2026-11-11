package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Resolve every component relative to an already opened directory. O_NOFOLLOW
// and device checks prevent path swaps from turning deletion into traversal.
func openDestroyParent(path string) (int, string, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == "/" {
		return -1, "", fmt.Errorf("unsafe deletion path")
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return -1, "", err
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}
func removeDestroyTree(path, identity string) error {
	observed, err := destroyPathIdentity(path, true)
	if err != nil {
		return err
	}
	if observed == "absent" {
		return nil
	}
	if observed != identity {
		return fmt.Errorf("filesystem target was replaced: %s", path)
	}
	parent, name, err := openDestroyParent(path)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	// Recheck the inode after opening the parent, so an ancestor rename cannot
	// redirect the removal away from the approved object.
	expected := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	if identity != expected && !strings.HasPrefix(identity, expected+":") {
		return fmt.Errorf("filesystem target changed before removal")
	}
	if err := unlinkDestroyEntry(parent, name, uint64(stat.Dev)); err != nil {
		return err
	}
	return unix.Fsync(parent)
}
func unlinkDestroyEntry(parent int, name string, device uint64) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return nil
		}
		return err
	}
	if uint64(stat.Dev) != device || stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("filesystem boundary changed during deletion")
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Unlinkat(parent, name, 0)
	}
	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_XDEV | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(fd), name)
	defer directory.Close()
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return err
	}
	if opened.Dev != stat.Dev || opened.Ino != stat.Ino {
		return fmt.Errorf("directory identity changed during deletion")
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := unlinkDestroyEntry(fd, entry.Name(), device); err != nil {
			return err
		}
	}
	if err := unix.Fsync(fd); err != nil {
		return err
	}
	return unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
}
