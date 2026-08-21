package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/topology"
)

const (
	defaultConfigPath   = "stacks/media/media-stack.yaml"
	checkedVersionsPath = "stacks/media/versions.yaml"
)

type PlanRequest struct {
	environment  string
	configPath   string
	versionsPath string
}

type Plan struct {
	compose []byte
}

type Engine interface {
	Plan(context.Context, PlanRequest) (Plan, error)
	Apply(context.Context, ApplyRequest) (ApplyReport, error)
	Backup(context.Context, BackupRequest) (BackupReport, error)
	Promote(context.Context, PromoteRequest) (PromoteReport, error)
	Restore(context.Context, RestoreRequest) (RestoreReport, error)
	Init(context.Context, InitRequest) (InitReport, error)
	Doctor(context.Context, DoctorRequest) (DoctorReport, error)
	Verify(context.Context, VerifyRequest) (VerifyReport, error)
}

type localEngine struct{}

func NewPlanRequest(workingDirectory, environment, configPath string) (PlanRequest, error) {
	repositoryRoot, err := findRepositoryRoot(workingDirectory)
	if err != nil {
		return PlanRequest{}, err
	}
	if configPath == "" {
		configPath = filepath.Join(repositoryRoot, filepath.FromSlash(defaultConfigPath))
	} else if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(workingDirectory, configPath)
	}
	return PlanRequest{
		environment:  environment,
		configPath:   configPath,
		versionsPath: filepath.Join(repositoryRoot, filepath.FromSlash(checkedVersionsPath)),
	}, nil
}

func New() Engine {
	return localEngine{}
}

func (localEngine) Plan(ctx context.Context, request PlanRequest) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	declared, err := config.Load(request.configPath)
	if err != nil {
		return Plan{}, err
	}
	if err := declared.ValidateEnvironment(request.environment); err != nil {
		return Plan{}, err
	}
	if len(declared.Spec.Acquisition.VPN.Server.Countries) == 0 {
		return Plan{}, fmt.Errorf("Declared Configuration requires at least one server country")
	}
	updateInterval, err := time.ParseDuration(declared.Spec.Acquisition.VPN.CatalogueUpdateInterval)
	if err != nil || updateInterval < 360*time.Hour {
		return Plan{}, fmt.Errorf("Declared Configuration catalogue update interval must be at least 360h")
	}
	versions, err := config.LoadVersions(request.versionsPath)
	if err != nil {
		return Plan{}, err
	}
	environment := declared.Spec.Environments[request.environment]
	compose, err := topology.Render(
		declared.Spec.Defaults,
		environment,
		declared.Spec.Acquisition.VPN,
		versions.Images,
		runtimeSecretDirectory(environment.ProjectName),
	)
	if err != nil {
		return Plan{}, err
	}
	return Plan{compose: compose}, nil
}

func runtimeSecretDirectory(projectName string) string {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" {
		root = filepath.Join(os.TempDir(), "media-stack-"+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(root, "media-stack", projectName)
}

func (plan Plan) Compose() []byte {
	return append([]byte(nil), plan.compose...)
}

func findRepositoryRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect repository root candidate %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("locate repository root from %q", start)
		}
		current = parent
	}
}
