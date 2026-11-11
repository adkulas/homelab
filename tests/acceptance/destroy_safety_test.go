package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/engine"
)

type destroyFixture struct {
	t                                      *testing.T
	root, temporary, configPath, statePath string
	declared                               config.MediaStack
	state                                  map[string]any
}

func newDestroyFixture(t *testing.T) *destroyFixture {
	t.Helper()
	root := repositoryRoot(t)
	tmp := t.TempDir()
	d, err := config.Load(filepath.Join(root, "tests/fixtures/media-stack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f := &destroyFixture{t: t, root: root, temporary: tmp, configPath: filepath.Join(tmp, "config.yaml"), statePath: filepath.Join(tmp, "docker.json"), declared: d}
	containers := map[string]any{}
	volumes := map[string]any{}
	networks := map[string]any{}
	for name, e := range d.Spec.Environments {
		e.ProjectName = fmt.Sprintf("destroy-%d-%s", time.Now().UnixNano(), name)
		e.DataRoot = filepath.Join(tmp, "data", name)
		e.BackupRoot = filepath.Join(tmp, "backup", name)
		e.SecretsFile = filepath.Join(tmp, "secrets", name+".sops.yaml")
		e.HardwareTranscoding = config.HardwareTranscodingDisabled
		d.Spec.Environments[name] = e
		for _, subtree := range []string{"media", "torrents"} {
			os.MkdirAll(filepath.Join(e.DataRoot, subtree), 0700)
			writeFile(t, filepath.Join(e.DataRoot, subtree, "payload"), []byte(name+" payload"), 0600)
		}
		os.MkdirAll(filepath.Dir(e.SecretsFile), 0700)
		writeFile(t, e.SecretsFile, []byte("ciphertext-"+name), 0600)
		volume := e.ProjectName + "_qbittorrent-config"
		storage := filepath.Join(tmp, "volumes", volume)
		os.MkdirAll(storage, 0700)
		writeFile(t, filepath.Join(storage, "state"), []byte("final-"+name), 0600)
		volumes[volume] = map[string]any{"Name": volume, "CreatedAt": "2026-11-10T00:00:00Z", "Mountpoint": storage, "Driver": "local", "Labels": map[string]string{"com.docker.compose.project": e.ProjectName, "com.docker.compose.volume": "qbittorrent-config"}}
		network := "network-" + name
		networks[network] = map[string]any{"Id": network, "Name": e.ProjectName + "_application", "Created": "2026-11-10T00:00:00Z", "Labels": map[string]string{"com.docker.compose.project": e.ProjectName, "com.docker.compose.network": "application"}}
		containers["container-"+name] = map[string]any{"Id": "container-" + name, "Name": e.ProjectName + "-qbittorrent", "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": e.ProjectName, "com.docker.compose.service": "qbittorrent", "com.docker.compose.oneoff": "False"}}, "State": map[string]bool{"Running": true}, "Mounts": []map[string]string{{"Type": "volume", "Name": volume}}, "network": network}
		runtime := filepath.Join(tmp, "runtime", "media-stack", e.ProjectName)
		os.MkdirAll(runtime, 0700)
		writeFile(t, filepath.Join(runtime, "openvpn_user"), []byte("runtime-secret-value"), 0600)
	}
	f.declared = d
	f.state = map[string]any{"containers": containers, "volumes": volumes, "networks": networks}
	f.save()
	if err := config.Write(f.configPath, d); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(tmp, "bin")
	os.Mkdir(bin, 0700)
	contents := readFile(t, filepath.Join(root, "tests/fixtures/destroy/docker.py"))
	writeExecutable(t, filepath.Join(bin, "docker"), string(contents))
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("DESTROY_FIXTURE", f.statePath)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmp, "runtime"))
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	return f
}
func (f *destroyFixture) save() {
	f.t.Helper()
	data, err := json.Marshal(f.state)
	if err != nil {
		f.t.Fatal(err)
	}
	writeFile(f.t, f.statePath, data, 0600)
}
func (f *destroyFixture) load() {
	f.t.Helper()
	if err := json.Unmarshal(readFile(f.t, f.statePath), &f.state); err != nil {
		f.t.Fatal(err)
	}
}
func (f *destroyFixture) preview(options engine.DestroyOptions) (engine.DestroyRequest, engine.DestroyReport) {
	f.t.Helper()
	r, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, options)
	if err != nil {
		f.t.Fatal(err)
	}
	r.ArtifactPath = filepath.Join(f.temporary, fmt.Sprintf("plan-%d.json", time.Now().UnixNano()))
	p, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		f.t.Fatal(err)
	}
	r.Execute = true
	r.Confirm = p.SHA256
	r.AcknowledgeLoss = p.Plan.LossAcknowledgement
	return r, p
}
func (f *destroyFixture) assertProduction() {
	f.t.Helper()
	f.load()
	containers := f.state["containers"].(map[string]any)
	if !containers["container-production"].(map[string]any)["State"].(map[string]any)["Running"].(bool) {
		f.t.Fatal("Production stopped")
	}
	e := f.declared.Spec.Environments["production"]
	if string(readFile(f.t, e.SecretsFile)) != "ciphertext-production" {
		f.t.Fatal("Production credentials changed")
	}
	for _, subtree := range []string{"media", "torrents"} {
		if string(readFile(f.t, filepath.Join(e.DataRoot, subtree, "payload"))) != "production payload" {
			f.t.Fatal("Production payload changed")
		}
	}
}
func TestDestroyEveryIndependentDeletionSelection(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			options := engine.DestroyOptions{Volumes: mask&1 != 0, Downloads: mask&2 != 0, Media: mask&4 != 0, Secrets: mask&8 != 0}
			if options.Secrets && !options.Volumes {
				if _, err := engine.NewDestroyRequest(repositoryRoot(t), "staging", "", options); err == nil {
					t.Fatal("secrets accepted without volumes")
				}
				return
			}
			f := newDestroyFixture(t)
			r, p := f.preview(options)
			if strings.Contains(string(readFile(t, r.ArtifactPath)), "runtime-secret-value") {
				t.Fatal("plan exposed secret value")
			}
			result, err := engine.New().Destroy(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Completed {
				t.Fatal("operation incomplete")
			}
			f.load()
			containers := f.state["containers"].(map[string]any)
			if _, exists := containers["container-staging"]; exists {
				t.Fatal("owned container retained")
			}
			e := f.declared.Spec.Environments["staging"]
			for _, tc := range []struct {
				path    string
				removed bool
			}{{e.SecretsFile, options.Secrets}, {filepath.Join(e.DataRoot, "torrents"), options.Downloads}, {filepath.Join(e.DataRoot, "media"), options.Media}, {filepath.Join(f.temporary, "runtime", "media-stack", e.ProjectName, "openvpn_user"), true}} {
				_, err := os.Stat(tc.path)
				if os.IsNotExist(err) != tc.removed {
					t.Fatalf("%s removed=%v, want %v", tc.path, os.IsNotExist(err), tc.removed)
				}
			}
			if _, err := os.Stat(e.DataRoot); err != nil {
				t.Fatal("dataRoot removed")
			}
			_, volumeExists := f.state["volumes"].(map[string]any)[e.ProjectName+"_qbittorrent-config"]
			if volumeExists == options.Volumes {
				t.Fatal("volume selection ignored")
			}
			if (result.BackupPath != "") != options.Volumes {
				t.Fatal("backup selection ignored")
			}
			if len(p.Plan.MissingVolumes) != 9 {
				t.Fatal("partial inventory lost")
			}
			f.assertProduction()
		})
	}
}

func TestDestroyRetainsUnknownRuntimeFiles(t *testing.T) {
	f := newDestroyFixture(t)
	e := f.declared.Spec.Environments["staging"]
	unknown := filepath.Join(f.temporary, "runtime", "media-stack", e.ProjectName, "operator-note")
	writeFile(t, unknown, []byte("retain unknown"), 0600)
	r, _ := f.preview(engine.DestroyOptions{})
	if _, err := engine.New().Destroy(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, unknown)) != "retain unknown" {
		t.Fatal("unknown runtime file removed")
	}
}

func TestDestroyFailureBeforeDeletionRecoversWritersAndAfterDeletionLeavesStopped(t *testing.T) {
	for _, phase := range []string{"stop", "create", "cp", "helper rm", "rm", "network rm", "volume rm"} {
		t.Run(phase, func(t *testing.T) {
			f := newDestroyFixture(t)
			r, _ := f.preview(engine.DestroyOptions{Volumes: true, Downloads: true, Secrets: true})
			f.load()
			f.state["fail"] = phase
			f.save()
			result, err := engine.New().Destroy(context.Background(), r)
			if err == nil || result.Completed || result.JournalPath == "" {
				t.Fatalf("injected %s failure: %+v %v", phase, result, err)
			}
			f.load()
			c, exists := f.state["containers"].(map[string]any)["container-staging"]
			beforeDeletion := phase == "stop" || phase == "create" || phase == "cp" || phase == "helper rm"
			if beforeDeletion && (!exists || !c.(map[string]any)["State"].(map[string]any)["Running"].(bool)) {
				t.Fatal("backup preparation failure did not restore running services")
			}
			if !beforeDeletion && exists && c.(map[string]any)["State"].(map[string]any)["Running"].(bool) {
				t.Fatal("post-deletion survivor restarted")
			}
			apply, _ := engine.NewApplyRequest(f.root, "staging", f.configPath)
			if _, err := engine.New().Apply(context.Background(), apply); err == nil || !strings.Contains(err.Error(), "unfinished destroy") {
				t.Fatalf("apply ignored operation gate: %v", err)
			}
			delete(f.state, "fail")
			f.save()
			result, err = engine.New().Destroy(context.Background(), r)
			if err != nil || !result.Completed {
				t.Fatalf("resume %s: %+v %v", phase, result, err)
			}
			f.assertProduction()
		})
	}
}

func TestDestroyRejectsUnsafeChangesBeforeStoppingServices(t *testing.T) {
	for _, change := range []string{"replacement container", "replacement volume", "unexpected consumer", "symlink", "remote context", "wrong daemon", "config", "versions", "unfinished restore", "Profilarr recovery"} {
		t.Run(change, func(t *testing.T) {
			f := newDestroyFixture(t)
			r, _ := f.preview(engine.DestroyOptions{Volumes: true, Downloads: true})
			e := f.declared.Spec.Environments["staging"]
			f.load()
			switch change {
			case "replacement container":
				cs := f.state["containers"].(map[string]any)
				c := cs["container-staging"].(map[string]any)
				delete(cs, "container-staging")
				c["Id"] = "replacement"
				cs["replacement"] = c
			case "replacement volume":
				f.state["volumes"].(map[string]any)[e.ProjectName+"_qbittorrent-config"].(map[string]any)["CreatedAt"] = "replacement"
			case "unexpected consumer":
				f.state["containers"].(map[string]any)["unknown"] = map[string]any{"Id": "unknown", "Config": map[string]any{"Labels": map[string]string{}}, "State": map[string]bool{"Running": true}, "Mounts": []map[string]string{{"Type": "volume", "Name": e.ProjectName + "_qbittorrent-config"}}}
			case "symlink":
				os.RemoveAll(filepath.Join(e.DataRoot, "torrents"))
				if err := os.Symlink(f.declared.Spec.Environments["production"].DataRoot, filepath.Join(e.DataRoot, "torrents")); err != nil {
					t.Fatal(err)
				}
			case "remote context":
				f.state["endpoint"] = "ssh://remote"
			case "wrong daemon":
				f.state["daemon"] = "another-daemon"
			case "config":
				file, err := os.OpenFile(f.configPath, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				file.WriteString("\n# changed declaration\n")
				file.Close()
			case "versions":
				r = r.WithDeployment(&engine.Deployment{Declared: f.declared, ConfigPath: f.configPath, ConfigurationIdentity: []byte("replacement configuration"), VersionsPath: filepath.Join(f.temporary, "versions.yaml")})
				writeFile(t, filepath.Join(f.temporary, "versions.yaml"), readFile(t, filepath.Join(f.root, "services/media/versions.yaml")), 0600)
			case "unfinished restore":
				directory := filepath.Join(e.BackupRoot, ".restore-operations")
				os.MkdirAll(directory, 0700)
				writeFile(t, filepath.Join(directory, "pending.json"), []byte(`{"id":"restore","status":"replacing"}`), 0600)
			case "Profilarr recovery":
				directory := filepath.Join(e.BackupRoot, "profilarr-bootstrap", e.ProjectName)
				os.MkdirAll(directory, 0700)
				writeFile(t, filepath.Join(directory, "pending.json"), []byte("pending"), 0600)
			}
			f.save()
			before := len(f.state["calls"].([]any))
			if _, err := engine.New().Destroy(context.Background(), r); err == nil {
				t.Fatalf("unsafe %s accepted", change)
			}
			f.load()
			for _, call := range f.state["calls"].([]any)[before:] {
				if call == "stop" {
					t.Fatal("unsafe operation stopped services")
				}
			}
		})
	}
}

func TestDestroyPartialSnapshotRejectsCorruptCaptureAndPreservesDrillCiphertext(t *testing.T) {
	f := newDestroyFixture(t)
	e := f.declared.Spec.Environments["staging"]
	gate := filepath.Join(e.BackupRoot, ".restore-drill")
	os.MkdirAll(gate, 0700)
	writeFile(t, filepath.Join(gate, "gate.json"), []byte(`{"schemaVersion":"homelab.media-stack/restore-drill-gate/v1alpha1","projectName":"`+e.ProjectName+`","confirmed":true}`), 0600)
	writeFile(t, filepath.Join(gate, "credentials.sops.yaml"), []byte("encrypted-drill-recovery"), 0600)
	writeBackupRetentionFixture(t, e.BackupRoot, "expired-unprotected", "2020-01-01T00:00:00Z", false)
	history := filepath.Join(e.BackupRoot, "historical-backup")
	os.MkdirAll(history, 0700)
	writeFile(t, filepath.Join(history, "keep"), []byte("history"), 0600)
	r, _ := f.preview(engine.DestroyOptions{Volumes: true})
	f.load()
	f.state["corrupt_second_stream"] = true
	f.save()
	if _, err := engine.New().Destroy(context.Background(), r); err == nil {
		t.Fatal("corrupted independent archive stream accepted")
	}
	f.load()
	if !f.state["containers"].(map[string]any)["container-staging"].(map[string]any)["State"].(map[string]any)["Running"].(bool) {
		t.Fatal("protection failure did not resume writer")
	}
	delete(f.state, "corrupt_second_stream")
	f.save()
	result, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	recovered := filepath.Join(filepath.Dir(result.BackupPath), "drill-credentials.sops.yaml")
	if string(readFile(t, recovered)) != "encrypted-drill-recovery" {
		t.Fatal("drill ciphertext missing")
	}
	if _, err := os.Stat(gate); !os.IsNotExist(err) {
		t.Fatal("obsolete drill gate retained")
	}
	if _, err := os.Stat(filepath.Join(e.BackupRoot, "expired-unprotected", "manifest.json")); err != nil {
		t.Fatal("retention deleted an expired historical backup")
	}
	if string(readFile(t, filepath.Join(history, "keep"))) != "history" {
		t.Fatal("retention ran during destroy")
	}
	if string(readFile(t, e.SecretsFile)) != "ciphertext-staging" {
		t.Fatal("unselected application ciphertext removed")
	}
}

func TestDestroyMissingTargetsDoNotCreateVolumesOrInventCompleteBackup(t *testing.T) {
	f := newDestroyFixture(t)
	e := f.declared.Spec.Environments["staging"]
	delete(f.state["volumes"].(map[string]any), e.ProjectName+"_qbittorrent-config")
	delete(f.state["containers"].(map[string]any), "container-staging")
	delete(f.state["networks"].(map[string]any), "network-staging")
	os.Remove(e.SecretsFile)
	os.Remove(filepath.Join(f.temporary, "runtime", "media-stack", e.ProjectName, "openvpn_user"))
	f.save()
	r, p := f.preview(engine.DestroyOptions{Volumes: true, Secrets: true})
	if len(p.Plan.MissingVolumes) != 10 {
		t.Fatal("missing coverage not explicit")
	}
	result, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.BackupPath != "" {
		t.Fatalf("invented Environment backup: %+v", result)
	}
	f.load()
	for _, call := range f.state["calls"].([]any) {
		if call == "create" || call == "volume create" {
			t.Fatal("missing volume recreated")
		}
	}
}

func TestDestroyRenewedPreviewResumesAfterDeletionAndVerifiesProtectedBackup(t *testing.T) {
	f := newDestroyFixture(t)
	r, _ := f.preview(engine.DestroyOptions{Volumes: true, Media: true})
	f.load()
	f.state["fail"] = "volume rm"
	f.save()
	interrupted, err := engine.New().Destroy(context.Background(), r)
	if err == nil {
		t.Fatal("injected failure accepted")
	}
	// Every pending lifecycle operation remains gated, including restore previews.
	restore, _ := engine.NewRestoreRequest(f.root, "staging", f.configPath, "missing", "", false, false, false)
	if _, err := engine.New().Restore(context.Background(), restore); err == nil || !strings.Contains(err.Error(), "unfinished destroy") {
		t.Fatalf("restore ignored destroy gate: %v", err)
	}
	f.load()
	delete(f.state, "fail")
	f.save()
	next, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, r.Options)
	if err != nil {
		t.Fatal(err)
	}
	next.Resume = interrupted.JournalPath
	next.ArtifactPath = filepath.Join(f.temporary, "renewed.json")
	preview, err := engine.New().Destroy(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	next.Execute = true
	next.Confirm = preview.SHA256
	next.AcknowledgeLoss = preview.Plan.LossAcknowledgement
	var backup engine.BackupReport
	if err := json.Unmarshal(readFile(t, interrupted.BackupPath), &backup); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(filepath.Dir(interrupted.BackupPath), backup.Services[0].ArchivePath)
	original := readFile(t, archive)
	writeFile(t, archive, []byte("corrupt"), 0600)
	if _, err := engine.New().Destroy(context.Background(), next); err == nil {
		t.Fatal("resume trusted a corrupted protected backup")
	}
	writeFile(t, archive, original, 0600)
	result, err := engine.New().Destroy(context.Background(), next)
	if err != nil || !result.Completed {
		t.Fatalf("renewed resume: %+v %v", result, err)
	}
	if result.BackupPath != interrupted.BackupPath {
		t.Fatal("resuming recaptured a different final state")
	}
	f.assertProduction()
}

func TestDestroyCancellationBeforeDeletionRecoversServicesAndReleasesLock(t *testing.T) {
	f := newDestroyFixture(t)
	r, _ := f.preview(engine.DestroyOptions{Volumes: true})
	f.load()
	f.state["delay"] = "cp"
	f.save()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	result, err := engine.New().Destroy(ctx, r)
	if err == nil || result.Completed {
		t.Fatalf("cancellation: %+v %v", result, err)
	}
	f.load()
	delete(f.state, "delay")
	f.save()
	if !f.state["containers"].(map[string]any)["container-staging"].(map[string]any)["State"].(map[string]any)["Running"].(bool) {
		t.Fatal("cancellation left writer stopped before deletion")
	}
	result, err = engine.New().Destroy(context.Background(), r)
	if err != nil || !result.Completed {
		t.Fatalf("resume after cancellation: %+v %v", result, err)
	}
}

func TestDestroyRejectsExpiredWrongHostAndMissingAcknowledgements(t *testing.T) {
	for _, change := range []string{"expired", "future", "Host", "options", "payload acknowledgement", "Production identity", "nested volume mount"} {
		t.Run(change, func(t *testing.T) {
			f := newDestroyFixture(t)
			r, p := f.preview(engine.DestroyOptions{Downloads: true})
			switch change {
			case "payload acknowledgement":
				r.AcknowledgeLoss = ""
			case "options":
				r.Options.Media = true
			case "Production identity":
				var err error
				r, err = engine.NewDestroyRequest(f.root, "production", f.configPath, engine.DestroyOptions{})
				if err != nil {
					t.Fatal(err)
				}
				r.ArtifactPath = filepath.Join(f.temporary, "production-plan.json")
				p, err = engine.New().Destroy(context.Background(), r)
				if err != nil {
					t.Fatal(err)
				}
				r.Execute = true
				r.Confirm = p.SHA256
			case "nested volume mount":
				r.Options = engine.DestroyOptions{Volumes: true}
				r.Execute = false
				r.ArtifactPath = filepath.Join(f.temporary, "mount-plan.json")
				f.load()
				e := f.declared.Spec.Environments["staging"]
				f.state["volumes"].(map[string]any)[e.ProjectName+"_qbittorrent-config"].(map[string]any)["Mountpoint"] = "/proc"
				f.save()
			default:
				artifact := p.Plan
				if change == "expired" {
					artifact.CreatedAt = time.Now().Add(-time.Hour)
					artifact.ExpiresAt = artifact.CreatedAt.Add(30 * time.Minute)
				}
				if change == "future" {
					artifact.CreatedAt = time.Now().Add(time.Hour)
					artifact.ExpiresAt = artifact.CreatedAt.Add(30 * time.Minute)
				}
				if change == "Host" {
					artifact.Host = "another-machine"
				}
				data, err := json.MarshalIndent(artifact, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, r.ArtifactPath, data, 0600)
				r.Confirm = fmt.Sprintf("%x", sha256.Sum256(data))
			}
			if _, err := engine.New().Destroy(context.Background(), r); err == nil {
				t.Fatalf("unsafe %s accepted", change)
			}
			f.load()
			for _, call := range f.state["calls"].([]any) {
				if call == "stop" {
					t.Fatal("refused operation stopped services")
				}
			}
		})
	}
}

func TestDestroyRejectsChangedVersionsAndIndependentEnvironmentCanMutate(t *testing.T) {
	f := newDestroyFixture(t)
	versions := filepath.Join(f.temporary, "versions.yaml")
	original := readFile(t, filepath.Join(f.root, "services/media/versions.yaml"))
	writeFile(t, versions, original, 0600)
	r, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, engine.DestroyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithDeployment(&engine.Deployment{Declared: f.declared, ConfigPath: f.configPath, VersionsPath: versions, ConfigurationIdentity: readFile(t, f.configPath)})
	r.ArtifactPath = filepath.Join(f.temporary, "versions-plan.json")
	preview, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, versions, append(original, []byte("\n# changed versions\n")...), 0600)
	r.Execute = true
	r.Confirm = preview.SHA256
	if _, err := engine.New().Destroy(context.Background(), r); err == nil || !strings.Contains(err.Error(), "versions") {
		t.Fatalf("changed versions accepted: %v", err)
	}
	// A pending Staging reset must not block Production's own lifecycle boundary.
	stage, _ := f.preview(engine.DestroyOptions{Volumes: true})
	f.load()
	f.state["fail"] = "volume rm"
	f.save()
	if _, err := engine.New().Destroy(context.Background(), stage); err == nil {
		t.Fatal("injected failure accepted")
	}
	production, err := engine.NewDestroyRequest(f.root, "production", f.configPath, engine.DestroyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	production.ArtifactPath = filepath.Join(f.temporary, "independent-production.json")
	p, err := engine.New().Destroy(context.Background(), production)
	if err != nil {
		t.Fatal(err)
	}
	production.Execute = true
	production.Confirm = p.SHA256
	production.Target = p.Plan.Target
	if result, err := engine.New().Destroy(context.Background(), production); err != nil || !result.Completed {
		t.Fatalf("Staging blocked Production: %+v %v", result, err)
	}
}

func TestDestroyNewPreviewCanAuthorizeReplacementWithFreshProtection(t *testing.T) {
	f := newDestroyFixture(t)
	r, _ := f.preview(engine.DestroyOptions{Volumes: true})
	f.load()
	f.state["fail"] = "volume rm"
	f.save()
	interrupted, err := engine.New().Destroy(context.Background(), r)
	if err == nil {
		t.Fatal("injected failure accepted")
	}
	f.load()
	delete(f.state, "fail")
	e := f.declared.Spec.Environments["staging"]
	volume := f.state["volumes"].(map[string]any)[e.ProjectName+"_qbittorrent-config"].(map[string]any)
	volume["CreatedAt"] = "newly-created-replacement"
	writeFile(t, filepath.Join(volume["Mountpoint"].(string), "state"), []byte("replacement final state"), 0600)
	f.save()
	if _, err := engine.New().Destroy(context.Background(), r); err == nil {
		t.Fatal("old authorization accepted replacement")
	}
	next, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, r.Options)
	if err != nil {
		t.Fatal(err)
	}
	next.ArtifactPath = filepath.Join(f.temporary, "replacement.json")
	next.Resume = interrupted.JournalPath
	preview, err := engine.New().Destroy(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	next.Execute = true
	next.Confirm = preview.SHA256
	result, err := engine.New().Destroy(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == interrupted.BackupPath || !result.Completed {
		t.Fatal("replacement did not receive fresh final-state protection")
	}
	if _, err := os.Stat(interrupted.BackupPath); err != nil {
		t.Fatal("previous destruction backup lost")
	}
}

func TestDestroyCompleteVolumeCoverageAndUnknownDockerResources(t *testing.T) {
	f := newDestroyFixture(t)
	e := f.declared.Spec.Environments["staging"]
	volumes := f.state["volumes"].(map[string]any)
	for _, service := range backupFixtureServiceNames {
		name := e.ProjectName + "_" + service + "-config"
		if _, exists := volumes[name]; exists {
			continue
		}
		storage := filepath.Join(f.temporary, "volumes", name)
		os.MkdirAll(storage, 0700)
		writeFile(t, filepath.Join(storage, "state"), []byte(service+" final state"), 0600)
		volumes[name] = map[string]any{"Name": name, "CreatedAt": "2026-11-10T00:00:00Z", "Driver": "local", "Mountpoint": storage, "Labels": map[string]string{"com.docker.compose.project": e.ProjectName, "com.docker.compose.volume": service + "-config"}}
	}
	unknownVolume := e.ProjectName + "_operator-state"
	volumes[unknownVolume] = map[string]any{"Name": unknownVolume}
	f.state["containers"].(map[string]any)["operator-container"] = map[string]any{"Id": "operator-container", "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": e.ProjectName, "com.docker.compose.service": "debug"}}, "State": map[string]bool{"Running": true}}
	f.state["networks"].(map[string]any)["operator-network"] = map[string]any{"Id": "operator-network", "Labels": map[string]string{"com.docker.compose.project": e.ProjectName, "com.docker.compose.network": "debug"}}
	f.save()
	r, p := f.preview(engine.DestroyOptions{Volumes: true})
	if len(p.Plan.MissingVolumes) != 0 {
		t.Fatal("complete inventory labelled missing")
	}
	result, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	var backup engine.BackupReport
	if err := json.Unmarshal(readFile(t, result.BackupPath), &backup); err != nil {
		t.Fatal(err)
	}
	if !backup.Complete || len(backup.Services) != 10 || len(backup.MissingVolumes) != 0 {
		t.Fatalf("complete coverage: %+v", backup)
	}
	f.load()
	for _, tc := range []struct{ key, name string }{{"containers", "operator-container"}, {"networks", "operator-network"}, {"volumes", unknownVolume}} {
		if _, exists := f.state[tc.key].(map[string]any)[tc.name]; !exists {
			t.Fatalf("unknown resource deleted: %s", tc.name)
		}
	}
}

func TestDestroyContainerOnlyRetainsPendingRecoveryState(t *testing.T) {
	f := newDestroyFixture(t)
	e := f.declared.Spec.Environments["staging"]
	paths := []string{filepath.Join(e.BackupRoot, ".restore-operations", "pending.json"), filepath.Join(e.BackupRoot, ".restore-drill", "credentials.sops.yaml"), filepath.Join(e.BackupRoot, "profilarr-bootstrap", e.ProjectName, "pending.json")}
	for _, path := range paths {
		os.MkdirAll(filepath.Dir(path), 0700)
		writeFile(t, path, []byte("pending recovery state"), 0600)
	}
	r, _ := f.preview(engine.DestroyOptions{})
	if _, err := engine.New().Destroy(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if string(readFile(t, path)) != "pending recovery state" {
			t.Fatal("container-only destruction changed recovery state")
		}
	}
}

func TestDestroyRetainsAgeIdentityInsideSelectedPayload(t *testing.T) {
	f := newDestroyFixture(t)
	e := f.declared.Spec.Environments["staging"]
	identity := filepath.Join(e.DataRoot, "torrents", "age-identity")
	writeFile(t, identity, []byte("private-age-identity"), 0600)
	t.Setenv("SOPS_AGE_KEY_FILE", identity)
	r, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, engine.DestroyOptions{Downloads: true})
	if err != nil {
		t.Fatal(err)
	}
	r.ArtifactPath = filepath.Join(f.temporary, "unsafe-identity.json")
	if _, err := engine.New().Destroy(context.Background(), r); err == nil {
		t.Fatal("payload selection authorized deleting the age identity")
	}
}

func TestDestroyFailureRestoresExecutionTimeRunningSet(t *testing.T) {
	for _, previewRunning := range []bool{false, true} {
		t.Run(fmt.Sprint(previewRunning), func(t *testing.T) {
			f := newDestroyFixture(t)
			f.state["containers"].(map[string]any)["container-staging"].(map[string]any)["State"].(map[string]bool)["Running"] = previewRunning
			f.save()
			r, _ := f.preview(engine.DestroyOptions{Volumes: true})
			f.load()
			c := f.state["containers"].(map[string]any)["container-staging"].(map[string]any)
			c["State"].(map[string]any)["Running"] = !previewRunning
			f.state["fail"] = "create"
			f.save()
			if _, err := engine.New().Destroy(context.Background(), r); err == nil {
				t.Fatal("injected failure accepted")
			}
			f.load()
			running := f.state["containers"].(map[string]any)["container-staging"].(map[string]any)["State"].(map[string]any)["Running"].(bool)
			if running != !previewRunning {
				t.Fatalf("recovery restored preview-time running=%v instead of execution-time running=%v", running, !previewRunning)
			}
		})
	}
}

func TestDestroyRechecksResolvedConfigurationDuringBackup(t *testing.T) {
	f := newDestroyFixture(t)
	reloads := 0
	deployment := &engine.Deployment{Declared: f.declared, ConfigPath: f.configPath, VersionsPath: filepath.Join(f.root, "services/media/versions.yaml"), ConfigurationIdentity: readFile(t, f.configPath)}
	deployment.Reload = func(ctx context.Context) (*engine.Deployment, error) {
		reloads++
		d, err := config.Load(f.configPath)
		if err != nil {
			return nil, err
		}
		fresh := *deployment
		fresh.Declared = d
		fresh.ConfigurationIdentity = readFile(t, f.configPath)
		return &fresh, nil
	}
	r, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, engine.DestroyOptions{Volumes: true})
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithDeployment(deployment)
	r.ArtifactPath = filepath.Join(f.temporary, "resolved.json")
	p, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	// The boundary fixture edits the actual declaration after volume capture.
	f.load()
	f.state["edit_config_on_cp"] = f.configPath
	f.save()
	r.Execute = true
	r.Confirm = p.SHA256
	if _, err := engine.New().Destroy(context.Background(), r); err == nil || !strings.Contains(err.Error(), "configuration") {
		t.Fatalf("cached configuration authorized deletion: %v", err)
	}
	if reloads < 2 {
		t.Fatal("resolved configuration was not re-read")
	}
	f.load()
	if _, exists := f.state["containers"].(map[string]any)["container-staging"]; !exists {
		t.Fatal("configuration change allowed container removal")
	}
}

func TestDestroyRejectsUnknownArtifactResourceKinds(t *testing.T) {
	f := newDestroyFixture(t)
	r, p := f.preview(engine.DestroyOptions{})
	p.Plan.Resources = append(p.Plan.Resources, engine.DestroyResource{Kind: "unknown-kind", Name: filepath.Join(f.temporary, "unapproved"), Identity: "absent"})
	data, err := json.Marshal(p.Plan)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, r.ArtifactPath, data, 0600)
	r.Confirm = fmt.Sprintf("%x", sha256.Sum256(data))
	if _, err := engine.New().Destroy(context.Background(), r); err == nil {
		t.Fatal("unknown artifact resource kind accepted")
	}
}

func TestDestroyBindsEffectiveResolvedConfiguration(t *testing.T) {
	f := newDestroyFixture(t)
	deployment := &engine.Deployment{Declared: f.declared, ConfigPath: f.configPath, VersionsPath: filepath.Join(f.root, "services/media/versions.yaml"), ConfigurationIdentity: readFile(t, f.configPath)}
	r, err := engine.NewDestroyRequest(f.root, "staging", f.configPath, engine.DestroyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithDeployment(deployment)
	r.ArtifactPath = filepath.Join(f.temporary, "effective.json")
	p, err := engine.New().Destroy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	deployment.Declared.Spec.Defaults.LANBindAddress = "127.0.0.2"
	r.Execute = true
	r.Confirm = p.SHA256
	if _, err := engine.New().Destroy(context.Background(), r); err == nil {
		t.Fatal("effective configuration change escaped declaration digest")
	}
}

func TestDestroySavedArtifactCannotDeleteSecretsWithoutVolumes(t *testing.T) {
	f := newDestroyFixture(t)
	r, p := f.preview(engine.DestroyOptions{Volumes: true, Secrets: true})
	p.Plan.Options.Volumes = false
	filtered := p.Plan.Resources[:0]
	for _, resource := range p.Plan.Resources {
		if resource.Kind != engine.DestroyVolume && resource.Kind != engine.DestroyDrillCredentials {
			filtered = append(filtered, resource)
		}
	}
	p.Plan.Resources = filtered
	data, err := json.Marshal(p.Plan)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, r.ArtifactPath, data, 0600)
	r.Options = engine.DestroyOptions{}
	r.Confirm = fmt.Sprintf("%x", sha256.Sum256(data))
	if _, err := engine.New().Destroy(context.Background(), r); err == nil || !strings.Contains(err.Error(), "--secrets requires --volumes") {
		t.Fatalf("invalid saved selection accepted: %v", err)
	}
	if string(readFile(t, f.declared.Spec.Environments["staging"].SecretsFile)) != "ciphertext-staging" {
		t.Fatal("invalid artifact removed application credentials")
	}
}
