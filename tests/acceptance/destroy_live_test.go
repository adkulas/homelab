package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/engine"
)

func TestDisposableDestroyProtectsPartialStateAndIsolatesProduction(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_DESTROY") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_DESTROY=1 for disposable Docker acceptance")
	}
	root := repositoryRoot(t)
	tmp := t.TempDir()
	declared, err := config.Load(filepath.Join(root, "tests/fixtures/media-stack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("destroy-acceptance-%d", time.Now().UnixNano())
	for name, e := range declared.Spec.Environments {
		e.ProjectName = prefix + "-" + name
		e.DataRoot = filepath.Join(tmp, "data", name)
		e.BackupRoot = filepath.Join(tmp, "backups", name)
		e.SecretsFile = filepath.Join(tmp, "secrets", name+".sops.yaml")
		e.HardwareTranscoding = config.HardwareTranscodingDisabled
		declared.Spec.Environments[name] = e
		os.MkdirAll(filepath.Dir(e.SecretsFile), 0700)
		for _, subtree := range []string{"media", "torrents"} {
			os.MkdirAll(filepath.Join(e.DataRoot, subtree), 0700)
			writeFile(t, filepath.Join(e.DataRoot, subtree, "sentinel"), []byte(name+" payload"), 0600)
		}
		writeFile(t, e.SecretsFile, []byte("encrypted-"+name), 0600)
	}
	configPath := filepath.Join(tmp, "config.yaml")
	if err := config.Write(configPath, declared); err != nil {
		t.Fatal(err)
	}
	stage := declared.Spec.Environments["staging"]
	prod := declared.Spec.Environments["production"]
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	volumes := []string{stage.ProjectName + "_qbittorrent-config", prod.ProjectName + "_qbittorrent-config"}
	var containers, networks []string
	t.Cleanup(func() {
		for _, id := range containers {
			exec.Command("docker", "rm", "-f", id).Run()
		}
		for _, id := range networks {
			exec.Command("docker", "network", "rm", id).Run()
		}
		for _, v := range volumes {
			exec.Command("docker", "volume", "rm", v).Run()
		}
	})
	for _, e := range []config.Environment{stage, prod} {
		volume := e.ProjectName + "_qbittorrent-config"
		docker("volume", "create", "--label", "com.docker.compose.project="+e.ProjectName, "--label", "com.docker.compose.volume=qbittorrent-config", volume)
		network := docker("network", "create", "--label", "com.docker.compose.project="+e.ProjectName, "--label", "com.docker.compose.network=application", e.ProjectName+"_application")
		networks = append(networks, network)
		id := docker("run", "-d", "--name", e.ProjectName+"-qbittorrent", "--network", network, "--label", "com.docker.compose.project="+e.ProjectName, "--label", "com.docker.compose.service=qbittorrent", "--label", "com.docker.compose.oneoff=False", "--volume", volume+":/state", "python:3.13-alpine", "sh", "-c", "printf final-state >/state/state; exec sleep 3600")
		containers = append(containers, id)
	}
	request, err := engine.NewDestroyRequest(root, "staging", configPath, engine.DestroyOptions{Volumes: true, Downloads: true, Secrets: true})
	if err != nil {
		t.Fatal(err)
	}
	request.ArtifactPath = filepath.Join(tmp, "plan.json")
	report, err := engine.New().Destroy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Plan.MissingVolumes) != 9 {
		t.Fatalf("missing declared volumes: %v", report.Plan.MissingVolumes)
	}
	request.Execute = true
	request.Confirm = report.SHA256
	request.AcknowledgeLoss = report.Plan.LossAcknowledgement
	// A newly attached unknown consumer must block before any owned service stops.
	external := docker("create", "--volume", volumes[0]+":/external", "python:3.13-alpine", "true")
	containers = append(containers, external)
	if _, err := engine.New().Destroy(context.Background(), request); err == nil {
		t.Fatal("unexpected live volume consumer accepted")
	}
	if docker("inspect", "--format", "{{.State.Running}}", containers[0]) != "true" {
		t.Fatal("preflight refusal stopped Staging")
	}
	docker("rm", external)
	// A subtree swapped for a symlink must also fail before stopping services.
	torrents := filepath.Join(stage.DataRoot, "torrents")
	parked := filepath.Join(stage.DataRoot, "parked-torrents")
	if err := os.Rename(torrents, parked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(prod.DataRoot, torrents); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.New().Destroy(context.Background(), request); err == nil {
		t.Fatal("live symlink escape accepted")
	}
	if docker("inspect", "--format", "{{.State.Running}}", containers[0]) != "true" {
		t.Fatal("symlink refusal stopped Staging")
	}
	if err := os.Remove(torrents); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parked, torrents); err != nil {
		t.Fatal(err)
	}

	result, err := engine.New().Destroy(context.Background(), request)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Completed || result.BackupPath == "" {
		t.Fatalf("incomplete destroy: %+v", result)
	}
	var backup engine.BackupReport
	if err := json.Unmarshal(readFile(t, result.BackupPath), &backup); err != nil {
		t.Fatal(err)
	}
	if backup.Complete || !backup.Protected || backup.Label != "before-destroy" || len(backup.Services) != 1 {
		t.Fatalf("partial coverage misreported: %+v", backup)
	}
	if _, err := os.Stat(stage.SecretsFile); !os.IsNotExist(err) {
		t.Fatal("active Staging credentials retained")
	}
	if _, err := os.Stat(filepath.Join(stage.DataRoot, "torrents")); !os.IsNotExist(err) {
		t.Fatal("selected downloads retained")
	}
	if got := string(readFile(t, filepath.Join(stage.DataRoot, "media", "sentinel"))); got != "staging payload" {
		t.Fatal("unselected media changed")
	}
	if got := string(readFile(t, prod.SecretsFile)); got != "encrypted-production" {
		t.Fatal("Production credentials changed")
	}
	if got := docker("inspect", "--format", "{{.State.Running}}", containers[1]); got != "true" {
		t.Fatal("Production stopped")
	}
	docker("volume", "inspect", volumes[1])
	docker("network", "inspect", networks[1])
	for _, subtree := range []string{"media", "torrents"} {
		if got := string(readFile(t, filepath.Join(prod.DataRoot, subtree, "sentinel"))); got != "production payload" {
			t.Fatal("Production payload changed")
		}
	}
	restore, _ := engine.NewRestoreRequest(root, "staging", configPath, result.BackupPath, "", false, false, false)
	if _, err := engine.New().Restore(context.Background(), restore); err == nil || !strings.Contains(err.Error(), "not a complete") {
		t.Fatalf("ordinary restore accepted partial snapshot: %v", err)
	}
}
