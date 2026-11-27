package engine

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/contractmodel"
)

//go:embed calibre_setup.py
var calibreSetup string

func prepareCalibreWeb(ctx context.Context, plan Plan, env config.Environment, defaults config.Defaults, password string) error {
	if password == "" {
		return fmt.Errorf("Calibre-Web requires the selected Environment household password")
	}
	if err := provisionDataLayout(env.DataRoot, defaults.RuntimeUID, defaults.RuntimeGID); err != nil {
		return err
	}
	// Compose normally consumes stdin. Use a private plan file so the bootstrap
	// container receives the household password on stdin before publishing its port.
	f, err := os.CreateTemp("", "calibre-compose-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(plan.Compose())
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", f.Name(), "run", "--rm", "--no-deps", "-T", "--entrypoint", "/lsiopy/bin/python", "calibre-web", "-c", calibreSetup)
	cmd.Stdin = strings.NewReader(password)
	output, err := cmd.Output()
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(output)), "calibre-ready") {
		return fmt.Errorf("prepare Calibre-Web local login and ebook conversion in %s: bootstrap did not confirm readiness (check the pinned image and volume permissions)", env.ProjectName)
	}
	if output, err := runDockerCompose(ctx, plan, "up", "-d", "--wait", "calibre-web"); err != nil {
		return fmt.Errorf("start Calibre-Web: %w: %s", err, output)
	}
	return nil
}

func observeCalibrePolicy(ctx context.Context, plan Plan) error {
	output, err := runDockerCompose(ctx, plan, "exec", "-T", "calibre-web", "/lsiopy/bin/python", "-c", calibreSetup, "observe")
	if err == nil {
		switch strings.TrimSpace(string(output)) {
		case "calibre-ready":
			return nil
		case "drift":
			return errPlannedMutation
		}
	}
	return fmt.Errorf("cannot read Calibre-Web conversion settings")
}

func CalibreWebConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Fixed("household.username", "Initial local login", "admin", "The image's initial local administrator owns the first household login.", "initialize"),
		contractmodel.SensitiveDerived("household.password", "Initial household password", "selected Stack Instance Environment SOPS document#jellyfin.password", "The initial local account uses this Environment's existing household password.", "After initialization, manage accounts and password changes in Calibre-Web.", "initialize"),
		contractmodel.Fixed("conversion.target", "Conversion target", "azw3", "Convert incoming EPUBs automatically and retain the EPUB on the same record.", "reconcile"),
		contractmodel.Fixed("conversion.retain", "Retained original format", "epub", "Keep the original EPUB attached to its converted book.", "reconcile"),
		contractmodel.Derived("library.path", "Calibre library", "spec.environments.<environment>.dataRoot + /books/calibre", "Calibre owns /calibre-library, including metadata.db and all book files. Storage backup preserves the directory as a unit.", "render", "preserve"),
		contractmodel.Derived("intake.path", "Watched ebook inbox", "spec.environments.<environment>.dataRoot + /books/ebooks/inbox", "Calibre watches /cwa-book-ingest. The operator publishes completed copies from the sibling ready directory.", "render"),
	}
}
