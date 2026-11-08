package siteinventory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adkulas/homelab/internal/config"
)

// Secret destinations may not exist yet, but every existing ancestor must remain
// inside the Site. Reject symlinks to keep reads and later atomic writes aligned.
func secretReference(siteDirectory, reference string) (string, error) {
	if reference == "" || filepath.IsAbs(reference) {
		return "", fmt.Errorf("secret reference must be a relative path within the selected Site")
	}
	candidate := filepath.Clean(filepath.Join(siteDirectory, reference))
	if !pathWithin(siteDirectory, candidate) || candidate == siteDirectory {
		return "", fmt.Errorf("secret reference escapes the selected Site")
	}
	relative, _ := filepath.Rel(siteDirectory, candidate)
	current := siteDirectory
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot inspect secret reference")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("secret references must not traverse symlinks")
		}
		if current == candidate && !info.Mode().IsRegular() {
			return "", fmt.Errorf("secret reference must name a regular file")
		}
	}
	return candidate, nil
}

func bindMediaSecrets(siteDirectory, site string, instance *StackInstance, declared *config.MediaStack) error {
	vpn, err := secretReference(siteDirectory, instance.Spec.Media.VPNSecretsFile)
	if err != nil {
		return fmt.Errorf("Stack Instance %s VPN secrets: %w", instance.Metadata.Name, err)
	}
	for name, environment := range declared.Spec.Environments {
		if environment.ProjectName == "" {
			return fmt.Errorf("Stack Instance %s %s requires a Compose project name", instance.Metadata.Name, name)
		}
		path, err := secretReference(siteDirectory, environment.SecretsFile)
		if err != nil {
			return fmt.Errorf("Stack Instance %s %s secrets: %w", instance.Metadata.Name, name, err)
		}
		environment.SecretsFile = path
		environment.VPNSecretsFile = vpn
		environment.SecretScope = &config.SecretScope{Site: site, Instance: string(instance.Metadata.Name), Environment: name}
		environment.RuntimeScope = filepath.Join(site, string(instance.Metadata.Name), name, environment.ProjectName)
		declared.Spec.Environments[name] = environment
	}
	return nil
}

func validateMediaSecretReferences(siteDirectory string, instances []StackInstance, report *Report) {
	applications := map[string]string{}
	vpns := map[string]bool{}
	for _, instance := range instances {
		if instance.Spec.Media == nil {
			continue
		}
		vpn, err := secretReference(siteDirectory, instance.Spec.Media.VPNSecretsFile)
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_SECRET_REFERENCE_INVALID", Explanation: err.Error(), Remedy: "Declare a Site-relative vpnSecretsFile and environment secretsFile paths without symlinks."})
		} else {
			vpns[vpn] = true
		}
		for _, name := range []string{"production", "staging"} {
			environment, exists := instance.Spec.Media.Environments[name]
			if !exists {
				continue
			}
			path, err := secretReference(siteDirectory, environment.SecretsFile)
			if err != nil {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_SECRET_REFERENCE_INVALID", Explanation: err.Error(), Remedy: "Keep application secret documents within the selected Site."})
				continue
			}
			owner := string(instance.Metadata.Name) + "/" + name
			if first, exists := applications[path]; exists {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_SECRET_CONFLICT", Explanation: fmt.Sprintf("%s and %s share an application secret document", first, owner), Remedy: "Use one application secret document per Stack Instance and Environment."})
			}
			applications[path] = owner
		}
	}
	for path := range applications {
		if vpns[path] {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_SECRET_CONFLICT", Explanation: "a VPN secret document is also used for application credentials", Remedy: "Use separate Site VPN and instance application secret documents."})
		}
	}
}

func writeInstance(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".instance-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
