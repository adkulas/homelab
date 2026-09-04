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

func Plan(ctx context.Context, repositoryRoot string, siteName SiteName, instanceName StackInstanceName, environment EnvironmentName) (PlanReport, error) {
	if !siteNamePattern.MatchString(string(siteName)) {
		return PlanReport{}, CodedError{Code: "SITE_NAME_INVALID", Explanation: fmt.Sprintf("Site name %q is not a safe semantic identifier.", siteName), Remedy: "Use lowercase letters, digits, and internal hyphens only."}
	}
	siteDirectory := filepath.Join(repositoryRoot, "sites", string(siteName))
	var site Site
	if err := decodeStrict(filepath.Join(siteDirectory, "site.yaml"), &site); err != nil {
		return PlanReport{}, codedDocumentError("Site", "site.yaml", err)
	}
	if site.APIVersion != siteAPIVersion || site.Kind != siteKind || site.Metadata.Name != siteName {
		return PlanReport{}, CodedError{Code: "SITE_DOCUMENT_UNSUPPORTED", Explanation: fmt.Sprintf("Site %q has unsupported identity, API version, or document kind.", siteName), Remedy: "Use a supported Site document with matching metadata.name."}
	}
	var instance StackInstance
	instanceReference, err := selectDocument(siteDirectory, site.Spec.StackInstances, string(instanceName), "Stack Instance", "SITE_STACK_INSTANCE_MISSING", &instance)
	if err != nil {
		return PlanReport{}, err
	}
	if instance.Spec.Stack != mediaStack || instance.Spec.Media == nil {
		return PlanReport{}, CodedError{Code: "SITE_STACK_UNSUPPORTED", Explanation: fmt.Sprintf("Stack Instance %q does not select the supported media Stack.", instanceName), Remedy: "Select the media Stack and provide its typed inputs."}
	}
	var host Host
	hostReference, err := selectDocument(siteDirectory, site.Spec.Hosts, string(instance.Spec.Host), "Host", "SITE_HOST_REFERENCE_MISSING", &host)
	if err != nil {
		return PlanReport{}, err
	}
	capabilities := map[Capability]bool{}
	for _, capability := range host.Spec.Capabilities {
		capabilities[capability] = true
	}
	for _, required := range []Capability{capabilityCompose, capabilityTUN, capabilityNetAdmin} {
		if !capabilities[required] {
			return PlanReport{}, CodedError{Code: "SITE_CAPABILITY_MISSING", Explanation: fmt.Sprintf("Host %q lacks capability %q required by Stack Instance %q.", host.Metadata.Name, required, instance.Metadata.Name), Remedy: "Declare a capable Host or move the Stack Instance to one."}
		}
	}
	declared, err := config.Load(filepath.Join(repositoryRoot, "stacks", "media", "media-stack.yaml"))
	if err != nil {
		return PlanReport{}, CodedError{Code: "SITE_STACK_POLICY_INVALID", Explanation: "the checked-in Media Stack policy could not be loaded.", Remedy: "Fix the checked-in Media Stack Declared Configuration."}
	}
	declared.Spec.Defaults = instance.Spec.Media.Defaults
	declared.Spec.Environments = instance.Spec.Media.Environments
	redactedReferences, err := resolveSensitiveInputs(ctx, siteDirectory, site.Spec.SensitiveValues, instance.Spec.Media, string(environment), &declared)
	if err != nil {
		return PlanReport{}, err
	}
	plan, err := engine.New().Plan(ctx, engine.NewResolvedPlanRequest(repositoryRoot, string(environment), declared))
	if err != nil {
		return PlanReport{}, CodedError{Code: "SITE_PLAN_FAILED", Explanation: "the selected Stack Instance and Environment could not be rendered.", Remedy: "Validate the typed Stack inputs and checked-in Stack policy."}
	}
	sources := []string{
		filepath.ToSlash(filepath.Join("sites", string(siteName), "site.yaml")),
		filepath.ToSlash(filepath.Join("sites", string(siteName), hostReference)),
		filepath.ToSlash(filepath.Join("sites", string(siteName), instanceReference)),
		"stacks/media/media-stack.yaml",
		"stacks/media/versions.yaml",
	}
	if len(redactedReferences) > 0 && site.Spec.SensitiveValues != nil {
		sources = append(sources, filepath.ToSlash(filepath.Join("sites", string(siteName), site.Spec.SensitiveValues.Document)))
	}
	return PlanReport{
		APIVersion: schemaVersion, Kind: "StackPlan", Site: siteName, Instance: instanceName,
		Host: instance.Spec.Host, Stack: instance.Spec.Stack, Environment: environment,
		Sources: sources, SensitiveReferences: redactedReferences, Plan: string(plan.Compose()),
	}, nil
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
