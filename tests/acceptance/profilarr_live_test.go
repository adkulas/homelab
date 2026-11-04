package acceptance_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adkulas/homelab/internal/profilarr"
)

// This test exercises the actual apply command and private adapter against the
// reviewed image. Only the external Arr HTTP APIs and unrelated services are fixtures.
func TestDisposablePinnedProfilarrApply(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_PROFILARR") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_PROFILARR=1 for the disposable pinned Profilarr apply acceptance test")
	}
	testApplyFlow(t, false, "fresh-profilarr-live")
}

func pinnedProfilarrFixture(t *testing.T) (string, string, string) {
	t.Helper()
	executable, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	name := "media-stack-profilarr-acceptance-" + strconv.Itoa(os.Getpid())
	volume := name + "-config"
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	_ = reservation.Close()
	output, err := exec.Command(executable, "run", "-d", "--name", name, "--mount", "type=volume,src="+volume+",dst=/config", "--add-host", "radarr:host-gateway", "--add-host", "sonarr:host-gateway", "-e", "AUTH=off", "-p", fmt.Sprintf("127.0.0.1:%d:6868", port), profilarr.SupportedImage).CombinedOutput()
	if err != nil {
		t.Fatalf("start disposable Profilarr: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(executable, "rm", "-f", name).Run()
		// Remove only helpers attached to this test's unique disposable volume.
		owners, _ := exec.Command(executable, "ps", "-a", "--filter", "volume="+volume, "--format", "{{.ID}}").Output()
		for _, owner := range strings.Fields(string(owners)) {
			_ = exec.Command(executable, "rm", "-f", owner).Run()
		}
		_ = exec.Command(executable, "volume", "rm", volume).Run()
	})
	address := publishedAddress(t, name, "6868/tcp")
	deadline := time.Now().Add(2 * time.Minute)
	pollService(t, deadline, "wait for disposable Profilarr", func() error {
		response, err := http.Get(address + "/api/v1/health")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		var state struct{ Status string }
		if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
			return err
		}
		if state.Status != "healthy" {
			return fmt.Errorf("not healthy")
		}
		return nil
	})
	return address, strings.TrimSpace(string(output)), executable
}

func applyArrServer(t *testing.T, live bool, port int, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	if live {
		listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		server.Listener = listener
	}
	server.Start()
	return server
}

// Behave like the supported Arr CRUD resources used by Profilarr. Independent
// expected profile content is asserted by apply's pinned policy fixtures.
func acceptProfilarrMutation(writer http.ResponseWriter, request *http.Request, profiles, formats, definitions *[]map[string]any, naming, media *map[string]any, delay *[]map[string]any) bool {
	path := strings.TrimPrefix(request.URL.Path, "/api/v3/")
	if request.Method == http.MethodGet && path == "delayprofile" {
		_ = json.NewEncoder(writer).Encode(*delay)
		return true
	}
	if request.Method != http.MethodPost && request.Method != http.MethodPut {
		return false
	}
	var target *[]map[string]any
	switch strings.Split(path, "/")[0] {
	case "qualityprofile":
		target = profiles
	case "customformat":
		target = formats
	case "delayprofile":
		target = delay
	case "qualitydefinition":
		if path != "qualitydefinition/update" {
			return false
		}
		_ = json.NewDecoder(request.Body).Decode(definitions)
		_ = json.NewEncoder(writer).Encode(*definitions)
		return true
	case "config":
		var doc map[string]any
		_ = json.NewDecoder(request.Body).Decode(&doc)
		switch {
		case strings.HasPrefix(path, "config/naming"):
			*naming = doc
		case strings.HasPrefix(path, "config/mediamanagement"):
			*media = doc
		default:
			return false
		}
		_ = json.NewEncoder(writer).Encode(doc)
		return true
	default:
		return false
	}
	var doc map[string]any
	_ = json.NewDecoder(request.Body).Decode(&doc)
	parts := strings.Split(path, "/")
	if len(parts) == 2 {
		id, _ := strconv.Atoi(parts[1])
		doc["id"] = float64(id)
	}
	if doc["id"] == nil {
		switch doc["name"] {
		case "HD Bluray + WEB":
			doc["id"] = float64(7)
		case "Remux 2160p (Alternative)":
			doc["id"] = float64(9)
		case "WEB-1080p":
			doc["id"] = float64(11)
		default:
			doc["id"] = float64(100 + len(*target))
		}
	}
	for i, existing := range *target {
		if existing["id"] == doc["id"] {
			(*target)[i] = doc
			_ = json.NewEncoder(writer).Encode(doc)
			return true
		}
	}
	*target = append(*target, doc)
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(doc)
	return true
}

func TestDisposablePinnedProfilarrRecovery(t *testing.T) {
	if os.Getenv("MEDIA_STACK_LIVE_PROFILARR") != "1" {
		t.Skip("set MEDIA_STACK_LIVE_PROFILARR=1 for disposable real-SQLite recovery tests")
	}
	for _, fault := range []string{"transaction", "restart", "verification", "schema", "migration", "partial", "custom-policy", "pcd-work", "interrupted", "helper-interrupted", "wal", "rollback"} {
		t.Run(fault, func(t *testing.T) { testApplyFlow(t, false, "fresh-profilarr-live-"+fault) })
	}
}

// Instrument the disposable helper only at the explicitly agreed database failure
// seam. No production failure flags, SQL snapshots, or durable credential payloads.
func installProfilarrFault(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/python3
import os,sys,json,subprocess,signal
args=sys.argv[1:]
payload=json.load(sys.stdin)
fault=os.environ['APPLY_PROFILARR_FAULT']
mode=payload['mode']
if fault in ['schema','migration','partial','custom-policy','pcd-work'] and mode=='inspect':
 sql={'schema':'CREATE TABLE unexpected(value TEXT)','migration':'DELETE FROM migrations WHERE version=76','partial':"INSERT INTO arr_instances(name,type,url,api_key) VALUES('Existing','radarr','http://unexpected:7878','x')",'custom-policy':"UPDATE pcd_ops SET sql='SELECT 1' WHERE id=(SELECT min(id) FROM pcd_ops)",'pcd-work':"INSERT INTO job_queue(job_type,status,run_at,payload,source) VALUES('pcd.link','queued','2999-01-01T00:00:00Z','{}','manual')"}[fault]
 args[-1]=args[-1].replace("open(input.mode === 'bootstrap');",'open(true); sql('+json.dumps(sql)+');')
if fault in ['transaction','wal','rollback'] and mode=='bootstrap':
 args[-1]=args[-1].replace("const id = sql('SELECT last_insert_rowid()')[0][0];","const id = sql('SELECT last_insert_rowid()')[0][0]; if (type === 'radarr') fail('mutation_failure');")
 args[-1]=args[-1].replace('console.log(JSON.stringify({ code }));',"console.log(JSON.stringify({code,atomic:sql('SELECT count(*) FROM arr_instances')[0][0]===0}));")
if fault=='wal' and mode=='backup':
 args[-1]=args[-1].replace("await command('tar', ['-cpf', archive, '-C', '/config', '.']);", "open(true); sql('PRAGMA journal_mode=WAL'); sql('UPDATE general_settings SET sync_prompt_enabled=0'); await command('tar', ['-cpf', archive, '-C', '/config', '.']);")
if fault=='rollback' and mode=='restore' :payload['archive']='bootstrap-00000000-0000-4000-8000-000000000000.tar'
if fault=='helper-interrupted' and mode=='bootstrap':
 args[-1]=args[-1].replace("const id = sql('SELECT last_insert_rowid()')[0][0];", "const id = sql('SELECT last_insert_rowid()')[0][0]; if(type === 'radarr') { console.log('transaction-open'); await new Promise(resolve=>setTimeout(resolve,60000)); }")
 process=subprocess.Popen([os.environ['APPLY_REAL_DOCKER'],*args],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 process.stdin.write(json.dumps(payload).encode());process.stdin.close()
 marker=process.stdout.readline()
 if marker.strip()==b'transaction-open':os.kill(os.getppid(),signal.SIGTERM)
 process.wait()
 sys.exit(1)
result=subprocess.run([os.environ['APPLY_REAL_DOCKER'],*args],input=json.dumps(payload).encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE)
try:
 doc=json.loads(result.stdout)
 if doc.get('atomic'):
  with open(os.environ['APPLY_PROFILARR_ATOMIC'],'w') as marker:marker.write('no connections committed')
except Exception:pass
sys.stdout.buffer.write(result.stdout)
# The real helper already strips errors. Never print the credential input or subprocess stderr.
sys.exit(result.returncode)
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func verifyProfilarrFault(t *testing.T, fault, id, docker, temporary, backups string, output []byte, runErr error, downstream bool) {
	t.Helper()
	code := map[string]string{"transaction": "MUTATION_FAILURE", "restart": "RESTART_FAILURE", "verification": "VERIFICATION_FAILURE", "schema": "UNSUPPORTED_SCHEMA", "migration": "UNSUPPORTED_SCHEMA", "partial": "NON_FRESH_STATE", "custom-policy": "NON_FRESH_STATE", "pcd-work": "NON_FRESH_STATE", "interrupted": "VERIFICATION_FAILURE", "helper-interrupted": "MUTATION_FAILURE", "wal": "MUTATION_FAILURE", "rollback": "ROLLBACK_FAILURE"}[fault]
	if runErr == nil || !strings.Contains(string(output), "PROFILARR_"+code) {
		t.Fatalf("expected %s refusal/recovery: %v\n%s", code, runErr, output)
	}
	if downstream {
		t.Fatal("failed bootstrap continued downstream setup")
	}
	for _, key := range []string{"fixture-radarr-api-key", "fixture-sonarr-api-key", "fixture-profilarr-api-key-32-characters"} {
		if strings.Contains(string(output), key) {
			t.Fatal("bootstrap failure exposed a credential")
		}
	}
	archives, _ := filepath.Glob(filepath.Join(backups, "*.tar"))
	if fault == "schema" || fault == "migration" || fault == "partial" || fault == "custom-policy" || fault == "pcd-work" {
		if len(archives) != 0 {
			t.Fatal("incompatible state reached backup/mutation")
		}
		return
	}
	if len(archives) != 1 {
		t.Fatal("recoverable pre-mutation archive missing")
	}
	info, err := os.Stat(archives[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("bootstrap archive must be private")
	}
	if fault == "transaction" || fault == "wal" || fault == "rollback" {
		if _, err := os.Stat(filepath.Join(temporary, "atomic")); err != nil {
			t.Fatal("actual SQLite transaction left partial connection state")
		}
	}
	if fault == "rollback" {
		if profilarr.CheckPending(backups) == nil {
			t.Fatal("failed recovery did not retain journal")
		}
		return
	}
	if profilarr.CheckPending(backups) != nil {
		t.Fatal("successful restore retained pending recovery")
	}
	state, err := exec.Command(docker, "inspect", "--format", "{{.State.Running}}", id).Output()
	if err != nil || strings.TrimSpace(string(state)) != "false" {
		t.Fatal("restored Profilarr must remain stopped")
	}
	actual, err := exec.Command(docker, "run", "--rm", "--network", "none", "--volumes-from", id+":ro", "--entrypoint", "tar", profilarr.SupportedImage, "-cf", "-", "-C", "/config", ".").Output()
	if err != nil {
		t.Fatal("could not inspect restored config")
	}
	archived, err := os.ReadFile(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	if fault == "wal" {
		manifest := tarManifest(t, archived)
		if _, ok := manifest["./data/profilarr.db-wal"]; !ok {
			t.Fatal("WAL recovery case did not archive WAL state")
		}
		if _, ok := manifest["./data/profilarr.db-shm"]; !ok {
			t.Fatal("WAL recovery case did not archive shared-memory state")
		}
	}
	if !reflect.DeepEqual(tarManifest(t, archived), tarManifest(t, actual)) {
		t.Fatal("restore did not reproduce complete archived state")
	}
}

func tarManifest(t *testing.T, data []byte) map[string]string {
	t.Helper()
	result := map[string]string{}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result[header.Name] = fmt.Sprintf("%d:%d:%d:%s:%x", header.Mode, header.Uid, header.Gid, header.Linkname, sha256.Sum256(content))
	}
}
