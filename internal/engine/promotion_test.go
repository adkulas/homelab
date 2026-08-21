package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPromoteAcceptsMatchingVerificationAndBackupArtifacts(t *testing.T) {
	temporary := t.TempDir()
	_, sourceFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	configPath := filepath.Join(temporary, "media-stack.yaml")
	versionsPath := filepath.Join(temporary, "versions.yaml")
	copyFile(t, filepath.Join(repoRoot, "stacks", "media", "media-stack.yaml"), configPath)
	copyFile(t, filepath.Join(repoRoot, "stacks", "media", "versions.yaml"), versionsPath)
	verificationPath := filepath.Join(temporary, "verification.json")
	backupPath := filepath.Join(temporary, "backup.json")
	verification := VerifyReport{SchemaVersion: verifySchemaVersion, Environment: "staging", Suite: "promotion", ConfigDigest: fileDigest(configPath), VersionsDigest: fileDigest(versionsPath)}
	backup := BackupReport{SchemaVersion: backupSchemaVersion, Environment: "staging"}
	writeJSON(t, verificationPath, verification)
	writeJSON(t, backupPath, backup)
	request, err := NewPromoteRequest(repoRoot, "production", configPath, verificationPath, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := New().Promote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Preview == "" || report.SchemaVersion != promotionSchemaVersion {
		t.Fatalf("unexpected promotion report: %#v", report)
	}
}

func TestPromoteRejectsMismatchedVerificationArtifact(t *testing.T) {
	temporary := t.TempDir()
	_, sourceFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	configPath := filepath.Join(temporary, "media-stack.yaml")
	versionsPath := filepath.Join(temporary, "versions.yaml")
	copyFile(t, filepath.Join(repoRoot, "stacks", "media", "media-stack.yaml"), configPath)
	copyFile(t, filepath.Join(repoRoot, "stacks", "media", "versions.yaml"), versionsPath)
	verificationPath := filepath.Join(temporary, "verification.json")
	backupPath := filepath.Join(temporary, "backup.json")
	verification := VerifyReport{SchemaVersion: verifySchemaVersion, Environment: "staging", Suite: "promotion", ConfigDigest: "bad", VersionsDigest: fileDigest(versionsPath)}
	backup := BackupReport{SchemaVersion: backupSchemaVersion, Environment: "staging"}
	writeJSON(t, verificationPath, verification)
	writeJSON(t, backupPath, backup)
	request, err := NewPromoteRequest(repoRoot, "production", configPath, verificationPath, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New().Promote(context.Background(), request); err == nil {
		t.Fatal("expected promotion to reject mismatched verification artifact")
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
