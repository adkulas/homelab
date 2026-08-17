package storageprobe

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

type Check struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Explanation string `json:"explanation,omitempty"`
	SourceInode uint64 `json:"sourceInode,omitempty"`
	TargetInode uint64 `json:"targetInode,omitempty"`
}

type Report struct {
	Checks []Check `json:"checks"`
}

func Run(source, destination string, uid, gid int) Report {
	report := Report{}
	add := func(name string, passed bool, explanation string) {
		report.Checks = append(report.Checks, Check{Name: name, Passed: passed, Explanation: explanation})
	}
	add("runtime_identity", os.Geteuid() == uid && os.Getegid() == gid,
		fmt.Sprintf("probe ran as %d:%d; declared identity is %d:%d", os.Geteuid(), os.Getegid(), uid, gid))

	token, err := randomToken()
	if err != nil {
		addUnavailableChecks(&report, "create unique probe name: "+err.Error())
		return report
	}
	sourceProbe := filepath.Join(source, ".media-stack-doctor-"+token+"-source")
	destinationProbe := filepath.Join(destination, ".media-stack-doctor-"+token+"-destination")
	sourceCreated := os.Mkdir(sourceProbe, 0o700) == nil
	destinationCreated := os.Mkdir(destinationProbe, 0o700) == nil
	if !sourceCreated || !destinationCreated {
		add("permissions", false, "create uniquely named probe directories")
		addUnavailableChecks(&report, "probe directories are not writable")
		add("cleanup", cleanup(sourceProbe, destinationProbe), "remove uniquely named probe directories")
		return report
	}

	fd, watchErr := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if watchErr == nil {
		defer syscall.Close(fd)
		watchMask := uint32(syscall.IN_CREATE | syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO)
		_, watchErr = syscall.InotifyAddWatch(fd, sourceProbe, watchMask)
		if watchErr == nil {
			_, watchErr = syscall.InotifyAddWatch(fd, destinationProbe, watchMask)
		}
	}

	sourceFile := filepath.Join(sourceProbe, "source")
	writeErr := os.WriteFile(sourceFile, []byte("media-stack storage probe\n"), 0o600)
	add("permissions", writeErr == nil, explain(writeErr))

	sourceDirectoryStat, sourceStatErr := stat(sourceProbe)
	destinationDirectoryStat, destinationStatErr := stat(destinationProbe)
	sameDevice := sourceStatErr == nil && destinationStatErr == nil &&
		sourceDirectoryStat.Dev == destinationDirectoryStat.Dev
	add("same_device", sameDevice, explain(firstError(sourceStatErr, destinationStatErr)))

	targetFile := filepath.Join(destinationProbe, "hardlink")
	linkErr := error(nil)
	if writeErr != nil {
		linkErr = writeErr
	} else {
		linkErr = os.Link(sourceFile, targetFile)
	}
	add("hardlink", linkErr == nil, explain(linkErr))

	sourceFileStat, sourceFileStatErr := stat(sourceFile)
	targetFileStat, targetFileStatErr := stat(targetFile)
	inodeIdentity := sourceFileStatErr == nil && targetFileStatErr == nil &&
		sourceFileStat.Dev == targetFileStat.Dev && sourceFileStat.Ino == targetFileStat.Ino
	inodeCheck := Check{
		Name: "inode_identity", Passed: inodeIdentity,
		Explanation: explain(firstError(sourceFileStatErr, targetFileStatErr)),
	}
	if sourceFileStatErr == nil {
		inodeCheck.SourceInode = sourceFileStat.Ino
	}
	if targetFileStatErr == nil {
		inodeCheck.TargetInode = targetFileStat.Ino
	}
	report.Checks = append(report.Checks, inodeCheck)

	renameSource := filepath.Join(destinationProbe, "rename-source")
	renameTarget := filepath.Join(destinationProbe, "rename-target")
	renameWriteErr := os.WriteFile(renameSource, []byte("rename probe\n"), 0o600)
	renameErr := renameWriteErr
	if renameErr == nil {
		renameErr = os.Rename(renameSource, renameTarget)
	}
	_, oldPathErr := os.Stat(renameSource)
	_, newPathErr := os.Stat(renameTarget)
	atomicRename := renameErr == nil && os.IsNotExist(oldPathErr) && newPathErr == nil
	add("atomic_rename", atomicRename, explain(firstError(renameErr, newPathErr)))

	eventsPassed := false
	eventsExplanation := ""
	if watchErr != nil {
		eventsExplanation = watchErr.Error()
	} else {
		eventsPassed, eventsExplanation = readRequiredEvents(fd)
	}
	add("filesystem_events", eventsPassed, eventsExplanation)
	add("cleanup", cleanup(sourceProbe, destinationProbe), "remove uniquely named probe directories")
	return report
}

func addUnavailableChecks(report *Report, explanation string) {
	for _, name := range []string{"same_device", "hardlink", "inode_identity", "atomic_rename", "filesystem_events"} {
		report.Checks = append(report.Checks, Check{Name: name, Passed: false, Explanation: explanation})
	}
}

func randomToken() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func stat(path string) (syscall.Stat_t, error) {
	var value syscall.Stat_t
	err := syscall.Stat(path, &value)
	return value, err
}

func cleanup(paths ...string) bool {
	ok := true
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			ok = false
		}
	}
	return ok
}

func firstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

func explain(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func readRequiredEvents(fd int) (bool, string) {
	buffer := make([]byte, 16*1024)
	count, err := syscall.Read(fd, buffer)
	if err != nil {
		return false, err.Error()
	}
	var seenCreate, seenMoveFrom, seenMoveTo bool
	for offset := 0; offset+syscall.SizeofInotifyEvent <= count; {
		event := (*syscall.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
		seenCreate = seenCreate || event.Mask&syscall.IN_CREATE != 0
		seenMoveFrom = seenMoveFrom || event.Mask&syscall.IN_MOVED_FROM != 0
		seenMoveTo = seenMoveTo || event.Mask&syscall.IN_MOVED_TO != 0
		offset += syscall.SizeofInotifyEvent + int(event.Len)
	}
	if !seenCreate || !seenMoveFrom || !seenMoveTo {
		return false, fmt.Sprintf("create=%t moved-from=%t moved-to=%t", seenCreate, seenMoveFrom, seenMoveTo)
	}
	return true, ""
}
