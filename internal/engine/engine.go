package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	Init(context.Context, InitRequest) (InitReport, error)
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
	versions, err := config.LoadVersions(request.versionsPath)
	if err != nil {
		return Plan{}, err
	}
	environment := declared.Spec.Environments[request.environment]
	if !filepath.IsAbs(environment.SecretsFile) {
		environment.SecretsFile = filepath.Join(filepath.Dir(request.configPath), environment.SecretsFile)
	}
	compose, err := topology.Render(declared.Spec.Defaults, environment, versions.Images)
	if err != nil {
		return Plan{}, err
	}
	return Plan{compose: compose}, nil
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
