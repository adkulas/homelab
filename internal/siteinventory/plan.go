package siteinventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/engine"
	"gopkg.in/yaml.v3"
)

type resolvedMedia struct {
	Deployment engine.Deployment
	Report     PlanReport
}

// ResolveMedia resolves a single explicit placement without decrypting application
// credentials. Storage references are resolved only for the selected Environment.
func ResolveMedia(ctx context.Context, repositoryRoot string, siteName SiteName, instanceName StackInstanceName, environment EnvironmentName) (*engine.Deployment, error) {
	resolved, err := resolveMedia(ctx, repositoryRoot, siteName, instanceName, environment, true)
	if err != nil {
		return nil, err
	}

	return &resolved.Deployment, nil
}

func Plan(ctx context.Context, repositoryRoot string, siteName SiteName, instanceName StackInstanceName, environment EnvironmentName) (PlanReport, error) {
	resolved, err := resolveMedia(ctx, repositoryRoot, siteName, instanceName, environment, false)
	if err != nil {
		return PlanReport{}, err
	}
	plan, err := engine.New().Plan(ctx, engine.NewResolvedPlanRequest(repositoryRoot, string(environment), resolved.Deployment.Declared))
	if err != nil {
		return PlanReport{}, CodedError{Code: "SITE_PLAN_FAILED", Explanation: "the selected Stack Instance and Environment could not be rendered.", Remedy: "Validate the typed Stack inputs and checked-in Stack policy."}
	}
	resolved.Report.Plan = string(plan.Compose())
	return resolved.Report, nil
}

func resolveMedia(ctx context.Context, repositoryRoot string, siteName SiteName, instanceName StackInstanceName, environment EnvironmentName, reveal bool) (resolvedMedia, error) {
	if !siteNamePattern.MatchString(string(instanceName)) {
		return resolvedMedia{}, fmt.Errorf("invalid Stack Instance identifier")
	}
	if !siteNamePattern.MatchString(string(siteName)) {
		return resolvedMedia{}, CodedError{Code: "SITE_NAME_INVALID", Explanation: fmt.Sprintf("Site name %q is not a safe semantic identifier.", siteName), Remedy: "Use lowercase letters, digits, and internal hyphens only."}
	}
	siteDirectory := filepath.Join(repositoryRoot, "sites", string(siteName))
	var site Site
	if err := decodeStrict(filepath.Join(siteDirectory, "site.yaml"), &site); err != nil {
		return resolvedMedia{}, codedDocumentError("Site", "site.yaml", err)
	}
	if site.APIVersion != siteAPIVersion || site.Kind != siteKind || site.Metadata.Name != siteName {
		return resolvedMedia{}, CodedError{Code: "SITE_DOCUMENT_UNSUPPORTED", Explanation: fmt.Sprintf("Site %q has unsupported identity, API version, or document kind.", siteName), Remedy: "Use a supported Site document with matching metadata.name."}
	}
	if err := validateMediaInventory(siteDirectory, site); err != nil {
		return resolvedMedia{}, err
	}
	var instance StackInstance
	instanceReference, err := selectDocument(siteDirectory, site.Spec.StackInstances, string(instanceName), "Stack Instance", "SITE_STACK_INSTANCE_MISSING", &instance)
	if err != nil {
		return resolvedMedia{}, err
	}
	if instance.APIVersion != siteAPIVersion || instance.Kind != instanceKind || instance.Spec.Stack != mediaStack || instance.Spec.Media == nil || instance.Spec.Network != nil {
		return resolvedMedia{}, CodedError{Code: "SITE_STACK_UNSUPPORTED", Explanation: fmt.Sprintf("Stack Instance %q does not select the supported media Stack.", instanceName), Remedy: "Select the media Stack and provide its typed inputs."}
	}
	var host Host
	hostReference, err := selectDocument(siteDirectory, site.Spec.Hosts, string(instance.Spec.Host), "Host", "SITE_HOST_REFERENCE_MISSING", &host)
	if err != nil {
		return resolvedMedia{}, err
	}
	if host.APIVersion != siteAPIVersion || host.Kind != hostKind || !siteNamePattern.MatchString(string(host.Metadata.Name)) {
		return resolvedMedia{}, fmt.Errorf("invalid Host identity")
	}
	capabilities := map[Capability]bool{}
	for _, capability := range host.Spec.Capabilities {
		capabilities[capability] = true
	}
	for _, required := range []Capability{capabilityCompose, capabilityTUN, capabilityNetAdmin} {
		if !capabilities[required] {
			return resolvedMedia{}, CodedError{Code: "SITE_CAPABILITY_MISSING", Explanation: fmt.Sprintf("Host %q lacks capability %q required by Stack Instance %q.", host.Metadata.Name, required, instance.Metadata.Name), Remedy: "Declare a capable Host or move the Stack Instance to one."}
		}
	}
	declared, err := config.Load(filepath.Join(repositoryRoot, "services", "media", "media-stack.yaml"))
	if err != nil {
		return resolvedMedia{}, CodedError{Code: "SITE_STACK_POLICY_INVALID", Explanation: "the checked-in Media Stack policy could not be loaded.", Remedy: "Fix the checked-in Media Stack Declared Configuration."}
	}
	declared.Spec.Defaults = instance.Spec.Media.Defaults
	declared.Spec.Environments = make(map[string]config.Environment)
	for name, value := range instance.Spec.Media.Environments {
		declared.Spec.Environments[name] = value
	}
	originalInstance, err := os.ReadFile(filepath.Join(siteDirectory, instanceReference))
	if err != nil {
		return resolvedMedia{}, err
	}
	configurationIdentity, err := yaml.Marshal(struct {
		Site        SiteName           `yaml:"site"`
		Instance    StackInstance      `yaml:"instance"`
		Host        Host               `yaml:"host"`
		Acquisition config.Acquisition `yaml:"acquisition"`
	}{siteName, instance, host, declared.Spec.Acquisition})
	if err != nil {
		return resolvedMedia{}, err
	}
	redactedReferences, err := resolveSensitiveInputs(ctx, siteDirectory, site.Spec.SensitiveValues, instance.Spec.Media, string(environment), &declared, reveal)
	if err != nil {
		return resolvedMedia{}, err
	}
	if err := bindMediaSecrets(siteDirectory, string(siteName), &instance, &declared); err != nil {
		return resolvedMedia{}, err
	}

	sources := []string{
		filepath.ToSlash(filepath.Join("sites", string(siteName), "site.yaml")),
		filepath.ToSlash(filepath.Join("sites", string(siteName), hostReference)),
		filepath.ToSlash(filepath.Join("sites", string(siteName), instanceReference)),
		"services/media/media-stack.yaml",
		"services/media/versions.yaml",
	}
	if len(redactedReferences) > 0 && site.Spec.SensitiveValues != nil {
		sources = append(sources, filepath.ToSlash(filepath.Join("sites", string(siteName), site.Spec.SensitiveValues.Document)))
	}
	deployment := engine.Deployment{Declared: declared, ConfigPath: filepath.Join(siteDirectory, instanceReference), VersionsPath: filepath.Join(repositoryRoot, "services/media/versions.yaml"), ConfigurationIdentity: configurationIdentity}
	deployment.Save = func(updated config.MediaStack) error {
		current, err := os.ReadFile(deployment.ConfigPath)
		if err != nil {
			return err
		}
		if string(current) != string(originalInstance) {
			return fmt.Errorf("Stack Instance changed concurrently; reload before initializing")
		}
		instance.Spec.Media.Defaults = updated.Spec.Defaults
		for name, value := range updated.Spec.Environments {
			original := instance.Spec.Media.Environments[name]
			original.HardwareTranscoding = value.HardwareTranscoding
			instance.Spec.Media.Environments[name] = original
		}
		contents, err := yaml.Marshal(instance)
		if err != nil {
			return err
		}
		return writeInstance(deployment.ConfigPath, contents)
	}
	return resolvedMedia{Deployment: deployment, Report: PlanReport{
		APIVersion: schemaVersion, Kind: "StackPlan", Site: siteName, Instance: instanceName,
		Host: instance.Spec.Host, Stack: instance.Spec.Stack, Environment: environment,
		Sources: sources, SensitiveReferences: redactedReferences,
	}}, nil
}

func selectDocument(siteDirectory string, references []string, name, kind, missingCode string, destination any) (string, error) {
	for _, reference := range references {
		path, diagnostic := resolveReference(siteDirectory, reference)
		if diagnostic != nil {
			continue
		}
		identity, err := decodeDocumentIdentity(path)
		if err != nil || identity != name {
			continue
		}
		if err := decodeStrict(path, destination); err != nil {
			return "", codedDocumentError(kind, reference, err)
		}
		return reference, nil
	}
	return "", CodedError{Code: missingCode, Explanation: fmt.Sprintf("%s %q is not explicitly referenced by the selected Site.", kind, name), Remedy: fmt.Sprintf("Select a %s explicitly referenced by the Site.", kind)}
}

func codedDocumentError(kind, reference string, err error) CodedError {
	diagnostic := documentError(kind, reference, err)
	return CodedError{Code: diagnostic.Code, Explanation: diagnostic.Explanation, Remedy: diagnostic.Remedy}
}

func decodeDocumentIdentity(path string) (string, error) {
	var document struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := yaml.NewDecoder(file).Decode(&document); err != nil {
		return "", err
	}
	return document.Metadata.Name, nil
}

func validateMediaInventory(siteDirectory string, site Site) error {
	report := Report{}
	_, instances := loadInventoryStructure(siteDirectory, site, &report)
	validateHostConflicts(instances, &report)
	validateMediaSecretReferences(siteDirectory, instances, &report)
	if len(report.Diagnostics) > 0 {
		diagnostic := report.Diagnostics[0]
		return CodedError{Code: diagnostic.Code, Explanation: diagnostic.Explanation, Remedy: diagnostic.Remedy}
	}
	return nil
}
