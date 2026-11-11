package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

// Deployment is the effective configuration selected by Site Inventory. It never
// contains decrypted credentials. Save updates only the instance declaration.
type Deployment struct {
	// Reload re-resolves authoritative Site declarations for volatile safety checks.
	Reload       func(context.Context) (*Deployment, error)
	HostName     string
	Declared     config.MediaStack
	ConfigPath   string
	VersionsPath string
	// ConfigurationIdentity contains canonical declarations, never decrypted values.
	ConfigurationIdentity []byte
	Save                  func(config.MediaStack) error
}

func (request PlanRequest) WithDeployment(deployment *Deployment) PlanRequest {
	if deployment != nil {
		request.declared = &deployment.Declared
		request.configPath = deployment.ConfigPath
		request.deployment = deployment
		if deployment.VersionsPath != "" {
			request.versionsPath = deployment.VersionsPath
		}
	}
	return request
}

func (request PlanRequest) load() (config.MediaStack, error) {
	if request.declared != nil {
		return *request.declared, nil
	}
	return config.Load(request.configPath)
}

func (request PlanRequest) configurationBytes() ([]byte, error) {
	if request.declared == nil {
		return os.ReadFile(request.configPath)
	}
	if request.deployment != nil {
		if len(request.deployment.ConfigurationIdentity) == 0 {
			return nil, fmt.Errorf("selected deployment has no configuration identity")
		}
		return append([]byte(nil), request.deployment.ConfigurationIdentity...), nil
	}
	return yaml.Marshal(request.declared)
}

func (request PlanRequest) configurationDigest() string {
	contents, err := request.configurationBytes()
	if err != nil {
		return ""
	}
	return checksum(contents)
}

func (request InitRequest) WithDeployment(deployment *Deployment) InitRequest {
	request.deployment = deployment
	if deployment != nil {
		request.configPath = deployment.ConfigPath
	}
	return request
}
func (request InitRequest) load() (config.MediaStack, error) {
	if request.deployment != nil {
		return request.deployment.Declared, nil
	}
	return config.Load(request.configPath)
}
func (request InitRequest) save(declared config.MediaStack) error {
	if request.deployment != nil {
		if request.deployment.Save == nil {
			return fmt.Errorf("selected deployment cannot save configuration")
		}
		return request.deployment.Save(declared)
	}
	return config.Write(request.configPath, declared)
}

func (request DoctorRequest) WithDeployment(deployment *Deployment) DoctorRequest {
	request.deployment = deployment
	if deployment != nil && deployment.VersionsPath != "" {
		request.versionsPath = deployment.VersionsPath
	}
	if deployment != nil {
		request.configPath = deployment.ConfigPath
	}
	return request
}
func (request DoctorRequest) load() (config.MediaStack, error) {
	if request.deployment != nil {
		return request.deployment.Declared, nil
	}
	return config.Load(request.configPath)
}

func (request ApplyRequest) WithDeployment(deployment *Deployment) ApplyRequest {
	request.plan = request.plan.WithDeployment(deployment)
	return request
}

func (request BackupRequest) WithDeployment(deployment *Deployment) BackupRequest {
	request.plan = request.plan.WithDeployment(deployment)
	return request
}

func (request RestoreRequest) WithDeployment(deployment *Deployment) RestoreRequest {
	request.plan = request.plan.WithDeployment(deployment)
	return request
}

func (request PromoteRequest) WithDeployment(deployment *Deployment) PromoteRequest {
	request.plan = request.plan.WithDeployment(deployment)
	return request
}

func (request VerifyRequest) WithDeployment(deployment *Deployment) VerifyRequest {
	request.plan = request.plan.WithDeployment(deployment)
	return request
}

func (request PlanRequest) refresh(ctx context.Context) (PlanRequest, error) {
	if request.deployment != nil && request.deployment.Reload != nil {
		fresh, err := request.deployment.Reload(ctx)
		if err != nil {
			return request, err
		}
		if fresh == nil {
			return request, fmt.Errorf("selected Site deployment could not be re-resolved")
		}
		return request.WithDeployment(fresh), nil
	}
	return request, nil
}
