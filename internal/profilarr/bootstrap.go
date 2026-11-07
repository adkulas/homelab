// Package profilarr isolates the temporary private-state initialization contract.
// All subsequent policy changes remain owned by Profilarr and its supported APIs.
package profilarr

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const SupportedImage = "ghcr.io/dictionarry-hub/profilarr@sha256:352e42dec74ecfd68d13bb198fbc6d9346e3a8c55003026e1814e59319a2176d"
const SupportedPolicy = "9e424382191de7d507efc9806ac3c807793d1c60"

//go:embed helper.ts
var helper string

type Error struct{ Code, Remedy string }

func (e *Error) Error() string             { return "PROFILARR_" + strings.ToUpper(e.Code) + ": " + e.Remedy }
func diagnostic(code, remedy string) error { return &Error{Code: code, Remedy: remedy} }

// Request contains only the selected Environment's topology and discovered keys.
// Verify must observe connections AND all policy gates via supported HTTP APIs.
type Request struct {
	Image, Revision, ContainerID, BackupDirectory string
	RadarrKey, SonarrKey                          string
	Verify                                        func(context.Context) error
}

// CheckPending prevents a crashed bootstrap from being mistaken for an established
// installation. Call before starting Profilarr, including on otherwise complete state.
func CheckPending(directory string) error {
	for _, name := range []string{"pending.json", "bootstrap.lock"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			return diagnostic("recovery_required", "an interrupted bootstrap requires recovery; keep Profilarr stopped and inspect "+directory+"; restore the recorded complete archive before clearing the journal and lock")
		} else if !os.IsNotExist(err) {
			return diagnostic("recovery_required", "cannot inspect the Profilarr bootstrap recovery journal")
		}
	}
	return nil
}

// Bootstrap never updates existing connections. It proves compatibility/freshness,
// backs up quiesced state, initializes once, and rolls back any unverified result.
func Bootstrap(ctx context.Context, request Request) (returnErr error) {
	if request.Image != SupportedImage || request.Revision != SupportedPolicy {
		return diagnostic("unsupported_version", "automatic bootstrap supports only the reviewed image and policy digests; use the guided Profilarr UI connection and policy setup, or restore compatible state")
	}
	if request.Verify == nil || request.ContainerID == "" || !filepath.IsAbs(request.BackupDirectory) {
		return diagnostic("precondition_failed", "bootstrap requires a container, absolute Environment backup directory, and supported-API verification")
	}
	if err := CheckPending(request.BackupDirectory); err != nil {
		return err
	}
	if err := os.MkdirAll(request.BackupDirectory, 0o700); err != nil {
		return diagnostic("backup_failure", "cannot create the Environment-specific bootstrap backup directory")
	}
	lock, err := os.OpenFile(filepath.Join(request.BackupDirectory, "bootstrap.lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return diagnostic("recovery_required", "another bootstrap or an interrupted bootstrap owns the recovery lock")
	}
	_ = lock.Close()
	defer os.Remove(filepath.Join(request.BackupDirectory, "bootstrap.lock"))
	if err := validateContainer(ctx, request); err != nil {
		return err
	}
	if err := stop(ctx, request.ContainerID); err != nil {
		return err
	}
	// Before a backup exists, refusal must leave the original state available.
	restartOnRefusal := true
	defer func() {
		if restartOnRefusal {
			recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := docker(recovery, nil, "start", request.ContainerID); err != nil {
				returnErr = fmt.Errorf("%w; original state was preserved but Profilarr could not restart; restart it before guided setup", returnErr)
			}
		}
	}()
	uuid := randomUUID()
	archive := "bootstrap-" + uuid + ".tar"
	payload := map[string]any{"archive": archive, "uuid": uuid, "revision": request.Revision}
	if err := runHelper(ctx, request, payload, "inspect"); err != nil {
		return err
	}
	if err := runHelper(ctx, request, payload, "backup"); err != nil {
		return diagnostic("backup_failure", "pre-mutation backup could not be verified; no bootstrap mutation was attempted")
	}
	journal, _ := json.Marshal(map[string]string{"archive": archive, "container": request.ContainerID, "image": request.Image})
	if err := persistJournal(request.BackupDirectory, journal); err != nil {
		return diagnostic("backup_failure", "cannot persist the recovery journal; no bootstrap mutation was attempted")
	}
	restartOnRefusal = false
	payload["radarrKey"], payload["sonarrKey"] = request.RadarrKey, request.SonarrKey
	mutationErr := runHelper(ctx, request, payload, "bootstrap")
	delete(payload, "radarrKey")
	delete(payload, "sonarrKey")
	if mutationErr == nil {
		if _, err := docker(ctx, nil, "start", request.ContainerID); err != nil {
			mutationErr = diagnostic("restart_failure", "Profilarr could not restart after initialization")
		}
	}
	if mutationErr == nil {
		if err := request.Verify(ctx); err != nil {
			mutationErr = diagnostic("verification_failure", "supported-API connection or policy verification failed; Arr may contain partially synchronized policy")
		}
	}
	if mutationErr != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := stop(recovery, request.ContainerID); err != nil {
			return diagnostic("rollback_failure", "could not prove Profilarr stopped; retain "+filepath.Join(request.BackupDirectory, archive)+" and pending.json for supervised recovery")
		}
		if err := runHelper(recovery, request, payload, "restore"); err != nil {
			return diagnostic("rollback_failure", "restore failed; Profilarr remains stopped; retain "+filepath.Join(request.BackupDirectory, archive)+" and pending.json for supervised recovery")
		}
		if err := os.Remove(filepath.Join(request.BackupDirectory, "pending.json")); err != nil {
			return diagnostic("rollback_failure", "state restored and stopped, but recovery journal could not be cleared")
		}
		return fmt.Errorf("%w; pre-bootstrap Profilarr state restored and stopped; backup: %s; Arr changes were not reversed; correct the cause and rerun apply", mutationErr, filepath.Join(request.BackupDirectory, archive))
	}
	if err := os.Remove(filepath.Join(request.BackupDirectory, "pending.json")); err != nil {
		return diagnostic("recovery_required", "verified initialization completed but the recovery journal could not be cleared")
	}
	return nil
}

func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type containerState struct {
	Image  string
	Config struct{ Image string }
	State  struct {
		Running bool
		Pid     int
		Status  string
	}
	Mounts []struct{ Destination, Type, Name string }
}

func inspect(ctx context.Context, id string) (containerState, error) {
	output, err := docker(ctx, nil, "inspect", id)
	var states []containerState
	if err != nil || json.Unmarshal(output, &states) != nil || len(states) != 1 {
		return containerState{}, diagnostic("precondition_failed", "cannot inspect the selected Profilarr container")
	}
	return states[0], nil
}
func validateContainer(ctx context.Context, request Request) error {
	state, err := inspect(ctx, request.ContainerID)
	if err != nil {
		return err
	}
	imageID, err := docker(ctx, nil, "image", "inspect", "--format", "{{.Id}}", SupportedImage)
	if err != nil || state.Config.Image != SupportedImage || strings.TrimSpace(string(imageID)) != state.Image {
		return diagnostic("unsupported_version", "running Profilarr artifact does not match the reviewed digest; use guided UI setup")
	}
	return nil
}
func stop(ctx context.Context, id string) error {
	if _, err := docker(ctx, nil, "stop", "--time", "30", id); err != nil {
		return diagnostic("stop_failure", "cannot stop Profilarr; no database operation is permitted")
	}
	state, err := inspect(ctx, id)
	if err != nil {
		return err
	}
	if state.State.Running || state.State.Pid != 0 || state.State.Status != "exited" {
		return diagnostic("stop_failure", "Profilarr is not proven stopped; no database operation is permitted")
	}
	volume := ""
	for _, mount := range state.Mounts {
		if mount.Destination == "/config" && mount.Type == "volume" {
			volume = mount.Name
		}
	}
	if volume == "" {
		return diagnostic("precondition_failed", "Profilarr requires its declared named /config volume")
	}
	output, err := docker(ctx, nil, "ps", "--filter", "volume="+volume, "--format", "{{.ID}}")
	if err != nil || strings.TrimSpace(string(output)) != "" {
		return diagnostic("stop_failure", "another running container may own the Profilarr configuration volume")
	}
	return nil
}
func runHelper(ctx context.Context, request Request, payload map[string]any, mode string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	helperName := "media-stack-profilarr-bootstrap-" + payload["uuid"].(string) + "-" + mode
	payload["mode"] = mode
	body, err := json.Marshal(payload)
	if err != nil {
		return diagnostic("mutation_failure", "cannot prepare private bootstrap payload")
	}
	network := "none"
	if mode == "bootstrap" {
		network = "bridge"
	}
	output, runErr := docker(ctx, body, "run", "--rm", "-i", "--name", helperName, "--network", network, "--volumes-from", request.ContainerID+":rw", "--mount", "type=bind,src="+request.BackupDirectory+",dst=/backup", "--entrypoint", "deno", SupportedImage, "eval", helper)
	if runErr != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = docker(cleanup, nil, "rm", "-f", helperName)
		cancel()
	}
	var result struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(bytes.TrimSpace(output), &result) != nil {
		return diagnostic("mutation_failure", "compatibility helper failed without a valid diagnostic; its output was suppressed to protect credentials")
	}
	if runErr == nil && result.Code == "ok" {
		return nil
	}
	switch result.Code {
	case "unsupported_schema", "unsupported_policy", "non_fresh_state", "migration_failure", "mutation_failure":
		return diagnostic(result.Code, "bootstrap refused or failed; use guided Profilarr setup or restore complete Environment-specific state; private helper output was suppressed")
	default:
		return diagnostic("mutation_failure", "compatibility helper failed; private output was suppressed")
	}
}
func docker(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	// Never include subprocess output/arguments in returned errors: stdin and
	// upstream diagnostic text may contain credentials.
	return command.CombinedOutput()
}

// The archive and journal must reach storage before private-state mutation.
func persistJournal(directory string, body []byte) error {
	path := filepath.Join(directory, "pending.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		parent, openErr := os.Open(directory)
		if openErr != nil {
			err = openErr
		} else {
			err = parent.Sync()
			_ = parent.Close()
		}
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
