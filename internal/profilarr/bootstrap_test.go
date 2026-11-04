package profilarr_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adkulas/homelab/internal/profilarr"
)

func TestBootstrapPreservesOrRestoresStateOnFailure(t *testing.T) {
	for _, scenario := range []struct {
		name, code string
		recovered  bool
	}{
		{"schema", "unsupported_schema", true}, {"partial", "non_fresh_state", true},
		{"running", "stop_failure", true}, {"shared-volume", "stop_failure", true},
		{"actual-image", "unsupported_version", true}, {"backup", "backup_failure", true},
		{"mutation", "mutation_failure", true}, {"restart", "restart_failure", true},
		{"verification", "verification_failure", true}, {"rollback", "rollback_failure", false},
		{"helper-secret", "mutation_failure", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request, state, trace := bootstrapFixture(t, scenario.name)
			request.Verify = func(context.Context) error { return errors.New("radarr-secret sonarr-secret") }
			err := profilarr.Bootstrap(context.Background(), request)
			var diagnostic *profilarr.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != scenario.code {
				t.Fatalf("got %v, want %s", err, scenario.code)
			}
			for _, secret := range []string{"radarr-secret", "sonarr-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("diagnostic exposed a credential")
				}
			}
			data, _ := os.ReadFile(state)
			if scenario.recovered && string(data) != "original" {
				t.Fatalf("failed bootstrap did not preserve original state: %s", data)
			}
			if !scenario.recovered {
				if profilarr.CheckPending(request.BackupDirectory) == nil {
					t.Fatal("ambiguous failed recovery was not retained")
				}
			}
			observed, _ := os.ReadFile(trace)
			if scenario.name == "schema" || scenario.name == "partial" || scenario.name == "running" || scenario.name == "shared-volume" || scenario.name == "actual-image" {
				if strings.Contains(string(observed), "bootstrap") || strings.Contains(string(observed), "backup\n") {
					t.Fatal("unsafe state reached backup or mutation")
				}
			}
		})
	}
}

func TestBootstrapRequiresSupportedArtifactBeforeAnyDockerOperation(t *testing.T) {
	request, _, trace := bootstrapFixture(t, "")
	request.Image = "ghcr.io/dictionarry-hub/profilarr:latest"
	err := profilarr.Bootstrap(context.Background(), request)
	var diagnostic *profilarr.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "unsupported_version" {
		t.Fatalf("got %v", err)
	}
	if data, _ := os.ReadFile(trace); len(data) != 0 {
		t.Fatal("unsupported image reached Docker")
	}
}

func TestBootstrapRetainsBackupAndClearsJournalOnlyAfterVerification(t *testing.T) {
	request, state, _ := bootstrapFixture(t, "")
	request.Verify = func(context.Context) error {
		if profilarr.CheckPending(request.BackupDirectory) == nil {
			t.Fatal("bootstrap was not marked pending during verification")
		}
		data, _ := os.ReadFile(state)
		if string(data) != "initialized" {
			t.Fatal("verification did not observe initialized state")
		}
		return nil
	}
	if err := profilarr.Bootstrap(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := profilarr.CheckPending(request.BackupDirectory); err != nil {
		t.Fatal(err)
	}
	archives, _ := filepath.Glob(filepath.Join(request.BackupDirectory, "*.tar"))
	if len(archives) != 1 {
		t.Fatal("recoverable archive was not retained")
	}
	info, err := os.Stat(archives[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("backup was not private")
	}
}

func TestBootstrapRecoveryJournalBlocksASecondAttempt(t *testing.T) {
	request, state, trace := bootstrapFixture(t, "")
	if err := os.MkdirAll(request.BackupDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(request.BackupDirectory, "pending.json"), []byte(`{"archive":"retained.tar"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := profilarr.Bootstrap(context.Background(), request); err == nil {
		t.Fatal("ambiguous prior bootstrap was accepted")
	}
	if data, _ := os.ReadFile(state); string(data) != "original" {
		t.Fatal("pending state changed")
	}
	if data, _ := os.ReadFile(trace); len(data) != 0 {
		t.Fatal("pending recovery reached Docker")
	}
}

func bootstrapFixture(t *testing.T, scenario string) (profilarr.Request, string, string) {
	t.Helper()
	temporary := t.TempDir()
	state := filepath.Join(temporary, "state")
	trace := filepath.Join(temporary, "trace")
	if err := os.WriteFile(state, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/python3
import os,sys,json
args=sys.argv[1:]
scenario=os.environ.get('PROFILARR_TEST_SCENARIO','')
state=os.environ['PROFILARR_TEST_STATE']
with open(os.environ['PROFILARR_TEST_TRACE'],'a') as log:
 log.write(args[0]+'\n')
if any('radarr-secret' in a or 'sonarr-secret' in a for a in args):sys.exit(99)
if args[0]=='inspect':
 print(json.dumps([{'Image':'wrong' if scenario=='actual-image' else 'reviewed-image','Config':{'Image':'` + profilarr.SupportedImage + `'},'State':{'Running':scenario=='running','Pid':1 if scenario=='running' else 0,'Status':'running' if scenario=='running' else 'exited'},'Mounts':[{'Destination':'/config','Type':'volume','Name':'selected-volume'}]}]))
elif args[:2]==['image','inspect']:print('reviewed-image')
elif args[0]=='ps':
 if scenario=='shared-volume':print('other-owner')
elif args[0]=='start':
 if scenario=='restart':sys.exit(1)
elif args[0]=='run':
 payload=json.load(sys.stdin)
 mode=payload['mode']
 with open(os.environ['PROFILARR_TEST_TRACE'],'a') as log:log.write(mode+'\n')
 if mode=='inspect' and scenario in ['schema','partial']:
  print(json.dumps({'code':'unsupported_schema' if scenario=='schema' else 'non_fresh_state'}));sys.exit(1)
 if mode=='backup':
  if scenario=='backup':print('{"code":"mutation_failure"}');sys.exit(1)
  mount=next(a for a in args if a.startswith('type=bind,src='))
  directory=mount.removeprefix('type=bind,src=').removesuffix(',dst=/backup')
  archive=os.path.join(directory,payload['archive'])
  with open(archive,'w') as f:f.write('original')
  os.chmod(archive,0o600)
 if mode=='bootstrap':
  if payload['radarrKey']!='radarr-secret' or payload['sonarrKey']!='sonarr-secret':sys.exit(99)
  with open(state,'w') as f:f.write('initialized')
  if scenario in ['mutation','helper-secret']:
   print('radarr-secret sonarr-secret' if scenario=='helper-secret' else '{"code":"mutation_failure"}');sys.exit(1)
 if mode=='restore':
  if scenario=='rollback':print('{"code":"mutation_failure"}');sys.exit(1)
  with open(state,'w') as f:f.write('original')
 print('{"code":"ok"}')
`
	binary := filepath.Join(temporary, "docker")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", temporary+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PROFILARR_TEST_SCENARIO", scenario)
	t.Setenv("PROFILARR_TEST_STATE", state)
	t.Setenv("PROFILARR_TEST_TRACE", trace)
	return profilarr.Request{Image: profilarr.SupportedImage, Revision: profilarr.SupportedPolicy, ContainerID: "selected-container", BackupDirectory: filepath.Join(temporary, "backup"), RadarrKey: "radarr-secret", SonarrKey: "sonarr-secret", Verify: func(context.Context) error { return nil }}, state, trace
}
