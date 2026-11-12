package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock files live outside repository/configuration paths so alternate selectors
// for the same Compose project share one OS lock. The kernel releases it on exit.
func lockEnvironment(ctx context.Context, plan PlanRequest, allowDestroy bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, err := plan.load()
	if err != nil {
		return nil, err
	}
	if err := d.ValidateEnvironment(plan.environment); err != nil {
		return nil, err
	}
	environment := d.Spec.Environments[plan.environment]

	// A fixed Host-wide location also coordinates operators with different UIDs,
	// TMPDIR values, repository checkouts and selectors. Lock files contain no data.
	directory := "/tmp/media-stack-environment-locks"
	if err := checkDestroyAncestors(directory); err != nil {
		return nil, err
	}
	if err := os.Mkdir(directory, os.ModeSticky|0777); err == nil {
		if err := os.Chmod(directory, os.ModeSticky|0777); err != nil {
			return nil, err
		}
	} else if !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0777 {
		return nil, fmt.Errorf("shared Environment lock directory must have sticky mode 1777")
	}
	path := filepath.Join(directory, checksum([]byte(environment.ProjectName))+".lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0444)
	if err == nil {
		if err := unix.Fchmod(fd, 0444); err != nil {
			unix.Close(fd)
			return nil, err
		}
	} else if err == unix.EEXIST {
		fd, err = unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		unix.Close(fd)
		return nil, fmt.Errorf("unsafe Environment lock file")
	}
	f := os.NewFile(uintptr(fd), "Environment lock")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s Environment is locked by another lifecycle operation", plan.environment)
	}
	release := func() { unix.Flock(fd, unix.LOCK_UN); f.Close() }
	if !allowDestroy {
		path := filepath.Join(destroyStateDirectory(environment), "active.json")
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			release()
			return nil, err
		}
		if err == nil {
			var active struct {
				Completed bool `json:"completed"`
			}
			if err := json.Unmarshal(data, &active); err != nil || !active.Completed {
				release()
				return nil, fmt.Errorf("unfinished destroy operation blocks lifecycle mutation; resume destroy using %s", path)
			}
		}
	}
	return release, nil
}
