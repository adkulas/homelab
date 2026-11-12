package engine

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/profilarr"
)

func createPrivateArtifact(path string, contents []byte) error {
	if err := checkDestroyAncestors(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(contents); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDirectory(filepath.Dir(path))
}
func readPrivateArtifact(path string) ([]byte, error) {
	if err := checkDestroyAncestors(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("artifact must be a private regular file")
	}
	return os.ReadFile(path)
}

func writeDestroyState(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := checkDestroyAncestors(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".destroy-write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func checkDestroyAncestors(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink boundary refused: %s", current)
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}
func destroyPathIdentity(path string, tree bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("destroy path must be absolute: %s", path)
	}
	if err := checkDestroyAncestors(path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("cannot identify storage object")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("unsupported storage object %s", path)
	}
	if err := checkDestroyMounts(path); err != nil {
		return "", err
	}
	if tree && info.IsDir() {
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in selected subtree: %s", current)
			}
			observed, err := entry.Info()
			if err != nil {
				return err
			}
			s, ok := observed.Sys().(*syscall.Stat_t)
			if !ok || s.Dev != stat.Dev {
				return fmt.Errorf("nested mount boundary: %s", current)
			}
			if !observed.IsDir() && !observed.Mode().IsRegular() {
				return fmt.Errorf("unsupported object in selected subtree: %s", current)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	identity := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	if info.Mode().IsRegular() {
		contents, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		identity += ":" + checksum(contents)
	}
	return identity, nil
}
func checkDestroyMounts(path string) error {
	contents, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("cannot establish filesystem mount boundaries: %w", err)
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mount := unescape.Replace(fields[4])
		if mount == path || strings.HasPrefix(mount, path+string(filepath.Separator)) {
			return fmt.Errorf("selected subtree contains a mount boundary: %s", mount)
		}
	}
	return nil
}
func destroyOverlaps(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func checkDestroyStorage(d config.MediaStack, selected string, resources []DestroyResource, configPath, versionsPath string) error {
	environment := d.Spec.Environments[selected]
	for name, other := range d.Spec.Environments {
		if name != selected && (other.ProjectName == environment.ProjectName || other.ProjectName == "") {
			return fmt.Errorf("destroy requires distinct nonempty Environment Compose projects")
		}
	}
	if environment.ProjectName == "" {
		return fmt.Errorf("destroy requires an explicit Compose project")
	}
	retained := []string{configPath, versionsPath, environment.VPNSecretsFile}
	if identity := os.Getenv("SOPS_AGE_KEY_FILE"); identity != "" {
		absolute, err := filepath.Abs(identity)
		if err != nil {
			return err
		}
		retained = append(retained, absolute)
	}
	if configDirectory, err := os.UserConfigDir(); err == nil {
		retained = append(retained, filepath.Join(configDirectory, "sops", "age", "keys.txt"))
	}
	for index, path := range retained {
		if path == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			retained[index] = resolved
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("cannot establish retained storage boundary")
		}
	}

	if !filepath.IsAbs(environment.SecretsFile) {
		environment.SecretsFile = selectedSecretPath(configPath, environment)
	}

	if !filepath.IsAbs(environment.BackupRoot) || destroyOverlaps(environment.BackupRoot, environment.DataRoot) {
		return fmt.Errorf("destroy requires an absolute backupRoot outside dataRoot")
	}
	if err := checkDestroyAncestors(environment.BackupRoot); err != nil {
		return err
	}
	for _, r := range resources {
		if r.Kind == DestroyContainer || r.Kind == DestroyNetwork || r.Kind == DestroyVolume {
			continue
		}
		for _, path := range retained {
			if path != "" && destroyOverlaps(r.Name, path) {
				return fmt.Errorf("selected deletion overlaps retained configuration, versions, VPN credentials or age identity")
			}
		}
		if r.Kind != DestroySecrets && destroyOverlaps(r.Name, environment.SecretsFile) {
			return fmt.Errorf("selected deletion overlaps application credentials without --secrets")
		}
		if r.Kind != DestroyDrillCredentials && destroyOverlaps(r.Name, environment.BackupRoot) {
			return fmt.Errorf("selected deletion overlaps retained backups")
		}
		if r.Kind == DestroySecrets && environment.VPNSecretsFile != "" && destroyOverlaps(r.Name, environment.VPNSecretsFile) {
			return fmt.Errorf("selected application document overlaps shared VPN credentials")
		}
		for name, other := range d.Spec.Environments {
			if name == selected {
				continue
			}
			paths := []string{other.DataRoot, other.BackupRoot, environmentRuntimeSecretDirectory(other)}
			secret := selectedSecretPath(configPath, other)
			paths = append(paths, secret)
			for _, retained := range paths {
				if retained != "" && destroyOverlaps(r.Name, retained) {
					return fmt.Errorf("selected deletion overlaps retained %s Environment storage", name)
				}
			}
		}
	}
	return nil
}
func checkDestroyRecovery(environment config.Environment) error {
	if err := profilarr.CheckPending(filepath.Join(environment.BackupRoot, "profilarr-bootstrap", environment.ProjectName)); err != nil {
		return err
	}
	directory := filepath.Join(environment.BackupRoot, ".restore-operations")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := readPrivateArtifact(filepath.Join(directory, entry.Name()))
		if err != nil {
			return err
		}
		var op restoreOperation
		if err := json.Unmarshal(data, &op); err != nil {
			return err
		}
		if op.Status != restoreCompleted && op.Status != restoreRolledBack {
			return fmt.Errorf("unfinished restore operation %s blocks volume destruction", op.ID)
		}
	}
	return nil
}
