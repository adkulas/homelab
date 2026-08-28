package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/adkulas/homelab/internal/qbittorrent"
)

func doctorQBittorrentBootstrap(ctx context.Context, image string, uid, gid int) error {
	probeContext, cancel := context.WithTimeout(ctx, doctorProbeTimeout)
	defer cancel()
	port, err := reserveLocalPort()
	if err != nil {
		return fmt.Errorf("reserve disposable Web UI port: %w", err)
	}
	suffix, err := randomProbeValue(9)
	if err != nil {
		return fmt.Errorf("generate disposable resource identity: %w", err)
	}
	name := "media-stack-qb-" + strings.ToLower(suffix)
	network := name + "-network"
	configDirectory, err := os.MkdirTemp("", "media-stack-qb-config-*")
	if err != nil {
		return fmt.Errorf("create disposable qBittorrent config: %w", err)
	}
	defer os.RemoveAll(configDirectory)
	defer runDockerCleanup(network, name)

	if output, err := dockerOutput(probeContext, nil, "network", "create", network); err != nil {
		return fmt.Errorf("create disposable qBittorrent network: %w: %s", err, bytes.TrimSpace(output))
	}
	arguments := []string{
		"run", "--detach", "--no-healthcheck", "--name", name,
		"--network", network, "--network-alias", "qbittorrent",
		"-e", "PUID=" + strconv.Itoa(uid), "-e", "PGID=" + strconv.Itoa(gid),
		"-e", "TZ=Etc/UTC", "-e", "WEBUI_PORT=" + strconv.Itoa(port),
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port),
		"--mount", "type=bind,source=" + configDirectory + ",target=/config",
		image,
	}
	if output, err := dockerOutput(probeContext, nil, arguments...); err != nil {
		return fmt.Errorf("start pinned qBittorrent image: %w: %s", err, bytes.TrimSpace(output))
	}
	declaredPassword, err := randomProbeValue(24)
	if err != nil {
		return fmt.Errorf("generate disposable declared credential: %w", err)
	}
	declared := qbittorrent.Credentials{Username: "media_stack_doctor", Password: declaredPassword}
	client := qbittorrent.New(fmt.Sprintf("http://127.0.0.1:%d", port), &http.Client{Timeout: 5 * time.Second})
	if err := client.Bootstrap(probeContext, declared, func(ctx context.Context) (string, bool, error) {
		// The probe always creates a new named container and config directory, so every
		// log entry belongs to this exact start and no historical credential is eligible.
		logs, logErr := dockerOutput(ctx, nil, "logs", name)
		if logErr != nil {
			return "", false, logErr
		}
		password, parseErr := temporaryQBittorrentPassword(logs)
		lowerLogs := strings.ToLower(string(logs))
		if parseErr != nil && (strings.Contains(lowerLogs, "password was not set") || strings.Contains(lowerLogs, "administrator username is: admin")) {
			return "", false, qbittorrent.ErrCurrentStartCredentialMissing
		}
		return password, parseErr == nil, nil
	}, 90*time.Second, time.Second); err != nil {
		return err
	}
	if output, err := dockerOutput(probeContext, nil, "restart", name); err != nil {
		return fmt.Errorf("restart pinned qBittorrent image: %w: %s", err, bytes.TrimSpace(output))
	}
	restarted := qbittorrent.New(fmt.Sprintf("http://127.0.0.1:%d", port), &http.Client{Timeout: 5 * time.Second})
	if err := restarted.Bootstrap(probeContext, declared, func(context.Context) (string, bool, error) { return "", false, nil }, 30*time.Second, time.Second); err != nil {
		return fmt.Errorf("verify declared credential after restart: %w", err)
	}
	if err := verifyQBittorrentPeerAuthentication(probeContext, image, network, port, declared); err != nil {
		return err
	}
	return nil
}

func verifyQBittorrentPeerAuthentication(ctx context.Context, image, network string, port int, credentials qbittorrent.Credentials) error {
	peerInput := []byte(credentials.Username + "\n" + credentials.Password + "\n")
	peerScript := `IFS= read -r user; IFS= read -r password; base="$1"; jar=$(mktemp); trap 'rm -f "$jar"' EXIT; curl --fail --silent --show-error --referer "$base" -c "$jar" --data-urlencode "username=$user" --data-urlencode "password=$password" "$base/api/v2/auth/login" >/dev/null; curl --fail --silent --show-error --referer "$base" -b "$jar" "$base/api/v2/app/version" >/dev/null`
	peerArguments := []string{"run", "--rm", "--no-healthcheck", "-i", "--network", network, "--entrypoint", "/bin/sh", image, "-c", peerScript, "probe", fmt.Sprintf("http://qbittorrent:%d", port)}
	if output, err := dockerOutput(ctx, peerInput, peerArguments...); err != nil {
		return fmt.Errorf("verify declared qBittorrent credential from application-network peer: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func reserveLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomProbeValue(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func dockerOutput(ctx context.Context, stdin []byte, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", arguments...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	return command.CombinedOutput()
}

func runDockerCleanup(network, container string) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = dockerOutput(cleanupContext, nil, "rm", "--force", container)
	_, _ = dockerOutput(cleanupContext, nil, "network", "rm", network)
}
