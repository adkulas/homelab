package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/engine"
)

func TestLifecycleCommandsShareEnvironmentLockAndCancellationReleasesIt(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	d, err := config.Load(filepath.Join(root, "tests/fixtures/media-stack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	for name, env := range d.Spec.Environments {
		env.ProjectName = "lock-" + filepath.Base(tmp) + "-" + name
		env.BackupRoot = filepath.Join(tmp, "backup", name)
		env.DataRoot = filepath.Join(tmp, "data", name)
		d.Spec.Environments[name] = env
	}
	path := filepath.Join(tmp, "config.yaml")
	if err := config.Write(path, d); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(tmp, "bin")
	os.Mkdir(bin, 0700)
	marker := filepath.Join(tmp, "entered")
	script := "#!/bin/sh\nprintf entered > \"$LOCK_TEST_MARKER\"\nexec sleep 20\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("LOCK_TEST_MARKER", marker)
	request, err := engine.NewBackupRequest(root, "staging", path, "", false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := engine.New().Backup(ctx, request); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not enter Docker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	apply, _ := engine.NewApplyRequest(root, "staging", path)
	_, err = engine.New().Apply(context.Background(), apply)
	if err == nil || !strings.Contains(err.Error(), "Environment is locked") {
		cancel()
		t.Fatalf("apply raced backup: %v", err)
	}
	restore, _ := engine.NewRestoreRequest(root, "staging", path, "missing", "", false, false, false)
	_, err = engine.New().Restore(context.Background(), restore)
	if err == nil || !strings.Contains(err.Error(), "Environment is locked") {
		t.Fatalf("restore raced backup: %v", err)
	}
	cancel()
	<-done
	canceled, cancelAgain := context.WithCancel(context.Background())
	cancelAgain()
	_, err = engine.New().Backup(canceled, request)
	if err == nil || strings.Contains(err.Error(), "Environment is locked") {
		t.Fatalf("cancellation retained lock: %v", err)
	}
}
