package siteinventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

const schemaVersion = "homelab.site/v1alpha1"

var siteNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

type APIVersion string
type DocumentKind string
type Capability string
type StackName string
type HostName string
type SiteName string
type StackInstanceName string
type EnvironmentName string
type RuntimeIdentity string
type SensitiveValueID string

const (
	siteAPIVersion APIVersion   = schemaVersion
	siteKind       DocumentKind = "Site"
	hostKind       DocumentKind = "Host"
	instanceKind   DocumentKind = "StackInstance"
	sensitiveKind  DocumentKind = "SiteSensitiveValues"
	mediaStack     StackName    = "media"

	capabilityCompose  Capability = "containers.compose"
	capabilityTUN      Capability = "network.tun"
	capabilityNetAdmin Capability = "network.net-admin"
)

type SiteMetadata struct {
	Name SiteName `yaml:"name"`
}

type HostMetadata struct {
	Name HostName `yaml:"name"`
}

type StackInstanceMetadata struct {
	Name StackInstanceName `yaml:"name"`
}

type Site struct {
	APIVersion APIVersion   `yaml:"apiVersion"`
	Kind       DocumentKind `yaml:"kind"`
	Metadata   SiteMetadata `yaml:"metadata"`
	Spec       SiteSpec     `yaml:"spec"`
}

type SiteSpec struct {
	Hosts           []string             `yaml:"hosts"`
	StackInstances  []string             `yaml:"stackInstances"`
	SensitiveValues *SensitiveValuesSpec `yaml:"sensitiveValues,omitempty"`
}

type SensitiveValuesSpec struct {
	Document          string `yaml:"document"`
	DailyRecipient    string `yaml:"dailyRecipient"`
	RecoveryRecipient string `yaml:"recoveryRecipient"`
}

type Host struct {
	APIVersion APIVersion   `yaml:"apiVersion"`
	Kind       DocumentKind `yaml:"kind"`
	Metadata   HostMetadata `yaml:"metadata"`
	Spec       HostSpec     `yaml:"spec"`
}

type HostSpec struct {
	Capabilities []Capability `yaml:"capabilities"`
}

type StackInstance struct {
	APIVersion APIVersion            `yaml:"apiVersion"`
	Kind       DocumentKind          `yaml:"kind"`
	Metadata   StackInstanceMetadata `yaml:"metadata"`
	Spec       StackInstanceSpec     `yaml:"spec"`
}

type StackInstanceSpec struct {
	Stack           StackName           `yaml:"stack"`
	Host            HostName            `yaml:"host"`
	RuntimeIdentity RuntimeIdentity     `yaml:"runtimeIdentity"`
	Media           *MediaStackInstance `yaml:"media,omitempty"`
}

type MediaStackInstance struct {
	Defaults            config.Defaults               `yaml:"defaults"`
	Environments        map[string]config.Environment `yaml:"environments"`
	SensitiveReferences SensitiveReferences           `yaml:"sensitiveReferences,omitempty"`
}

type SensitiveReferences struct {
	Environments map[string]EnvironmentSensitiveReferences `yaml:"environments,omitempty"`
}

type EnvironmentSensitiveReferences struct {
	DataRoot                 SensitiveValueID `yaml:"dataRoot,omitempty"`
	DataRootClaim            string           `yaml:"dataRootClaim,omitempty"`
	DataRootAncestorClaims   []string         `yaml:"dataRootAncestorClaims,omitempty"`
	BackupRoot               SensitiveValueID `yaml:"backupRoot,omitempty"`
	BackupRootClaim          string           `yaml:"backupRootClaim,omitempty"`
	BackupRootAncestorClaims []string         `yaml:"backupRootAncestorClaims,omitempty"`
}

type Diagnostic struct {
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
	Remedy      string `json:"remedy,omitempty"`
}

type Report struct {
	APIVersion     string       `json:"apiVersion"`
	Kind           string       `json:"kind"`
	Site           string       `json:"site"`
	Valid          bool         `json:"valid"`
	Hosts          int          `json:"hosts"`
	StackInstances int          `json:"stackInstances"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
}

type PlanReport struct {
	APIVersion          string              `json:"apiVersion"`
	Kind                string              `json:"kind"`
	Site                SiteName            `json:"site"`
	Instance            StackInstanceName   `json:"instance"`
	Host                HostName            `json:"host"`
	Stack               StackName           `json:"stack"`
	Environment         EnvironmentName     `json:"environment"`
	Sources             []string            `json:"sources"`
	SensitiveReferences []RedactedReference `json:"sensitiveReferences,omitempty"`
	Plan                string              `json:"plan"`
}

type RedactedReference struct {
	ID    SensitiveValueID `json:"id"`
	Value string           `json:"value"`
}

type SiteSensitiveValues struct {
	APIVersion APIVersion              `yaml:"apiVersion"`
	Kind       DocumentKind            `yaml:"kind"`
	Spec       SiteSensitiveValuesSpec `yaml:"spec"`
}

type SiteSensitiveValuesSpec struct {
	Values []SiteSensitiveValue `yaml:"values"`
}

type SiteSensitiveValue struct {
	ID    SensitiveValueID `yaml:"id"`
	Value string           `yaml:"value"`
}

type CodedError struct {
	Code        string
	Explanation string
	Remedy      string
}

func (failure CodedError) Error() string {
	return fmt.Sprintf("%s %s; remedy: %s", failure.Code, failure.Explanation, failure.Remedy)
}

func (report Report) Human() string {
	if report.Valid {
		instanceLabel := "Stack Instances"
		if report.StackInstances == 1 {
			instanceLabel = "Stack Instance"
		}
		return fmt.Sprintf("SITE_VALID valid Site %s: %d Hosts, %d %s\n", report.Site, report.Hosts, report.StackInstances, instanceLabel)
	}
	result := ""
	for _, diagnostic := range report.Diagnostics {
		result += fmt.Sprintf("%s %s; remedy: %s\n", diagnostic.Code, diagnostic.Explanation, diagnostic.Remedy)
	}
	return result
}

func Validate(ctx context.Context, repositoryRoot string, siteName SiteName) Report {
	report := Report{APIVersion: schemaVersion, Kind: "SiteValidation", Site: string(siteName), Valid: false, Diagnostics: []Diagnostic{}}
	if err := ctx.Err(); err != nil {
		report.Diagnostics = []Diagnostic{{Code: "SITE_VALIDATION_CANCELLED", Explanation: "Site validation was cancelled.", Remedy: "Retry the command."}}
		return report
	}
	if !siteNamePattern.MatchString(string(siteName)) {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_NAME_INVALID", Explanation: fmt.Sprintf("Site name %q is not a safe semantic identifier.", siteName), Remedy: "Use lowercase letters, digits, and internal hyphens only."})
		return report
	}
	siteDirectory := filepath.Join(repositoryRoot, "sites", string(siteName))
	var site Site
	if err := decodeStrict(filepath.Join(siteDirectory, "site.yaml"), &site); err != nil {
		report.Diagnostics = []Diagnostic{documentError("Site", "site.yaml", err)}
		return report
	}
	if site.APIVersion != siteAPIVersion || site.Kind != siteKind || site.Metadata.Name != siteName {
		report.Diagnostics = []Diagnostic{{Code: "SITE_DOCUMENT_UNSUPPORTED", Explanation: fmt.Sprintf("Site %q has unsupported identity, API version, or document kind.", siteName), Remedy: "Use apiVersion homelab.site/v1alpha1, kind Site, and matching metadata.name."}}
		return report
	}
	if site.Spec.SensitiveValues != nil {
		sensitive := site.Spec.SensitiveValues
		if sensitive.DailyRecipient == "" || sensitive.RecoveryRecipient == "" || sensitive.DailyRecipient == sensitive.RecoveryRecipient {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_SENSITIVE_RECIPIENTS_INVALID", Explanation: "Site-sensitive values require distinct daily and recovery recipients.", Remedy: "Declare two distinct age recipients for the Site."})
		}
		if path, diagnostic := resolveReference(siteDirectory, sensitive.Document); diagnostic != nil {
			report.Diagnostics = append(report.Diagnostics, *diagnostic)
		} else if !encryptedDocumentHasRecipients(path, sensitive.DailyRecipient, sensitive.RecoveryRecipient) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_SENSITIVE_RECIPIENTS_MISMATCH", Explanation: "the Site-sensitive values document does not include both declared recipients.", Remedy: "Re-encrypt the document for the declared daily and recovery recipients."})
		}
	}
	hostNames := map[HostName]string{}
	hosts := map[HostName]Host{}
	for _, reference := range site.Spec.Hosts {
		path, diagnostic := resolveReference(siteDirectory, reference)
		if diagnostic != nil {
			report.Diagnostics = append(report.Diagnostics, *diagnostic)
			continue
		}
		var host Host
		if err := decodeStrict(path, &host); err != nil {
			report.Diagnostics = append(report.Diagnostics, documentError("Host", reference, err))
			continue
		}
		if host.APIVersion != siteAPIVersion || host.Kind != hostKind || host.Metadata.Name == "" {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_DOCUMENT_UNSUPPORTED", Explanation: fmt.Sprintf("Host reference %q has an unsupported API version, kind, or empty name.", reference), Remedy: "Use apiVersion homelab.site/v1alpha1 and kind Host with a name."})
			continue
		}
		if first, exists := hostNames[host.Metadata.Name]; exists {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_IDENTIFIER_DUPLICATE", Explanation: fmt.Sprintf("Host identifier %q is declared by both %q and %q.", host.Metadata.Name, first, reference), Remedy: "Give every Host within the Site a unique name."})
		} else {
			hostNames[host.Metadata.Name] = reference
			hosts[host.Metadata.Name] = host
		}
		seenCapabilities := map[Capability]bool{}
		for _, capability := range host.Spec.Capabilities {
			if !knownCapability(capability) {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_CAPABILITY_UNKNOWN", Explanation: fmt.Sprintf("Host %q declares unknown capability %q.", host.Metadata.Name, capability), Remedy: "Use a capability from the homelab.site/v1alpha1 capability catalog."})
			} else if seenCapabilities[capability] {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_CAPABILITY_DUPLICATE", Explanation: fmt.Sprintf("Host %q declares capability %q more than once.", host.Metadata.Name, capability), Remedy: "Declare each Host capability once."})
			}
			seenCapabilities[capability] = true
		}
	}
	instanceNames := map[StackInstanceName]string{}
	instances := make([]StackInstance, 0, len(site.Spec.StackInstances))
	for _, reference := range site.Spec.StackInstances {
		path, diagnostic := resolveReference(siteDirectory, reference)
		if diagnostic != nil {
			report.Diagnostics = append(report.Diagnostics, *diagnostic)
			continue
		}
		var instance StackInstance
		if err := decodeStrict(path, &instance); err != nil {
			report.Diagnostics = append(report.Diagnostics, documentError("Stack Instance", reference, err))
			continue
		}
		if instance.APIVersion != siteAPIVersion || instance.Kind != instanceKind || instance.Metadata.Name == "" {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_DOCUMENT_UNSUPPORTED", Explanation: fmt.Sprintf("Stack Instance reference %q has an unsupported API version, kind, or empty name.", reference), Remedy: "Use apiVersion homelab.site/v1alpha1 and kind StackInstance with a name."})
			continue
		}
		if first, exists := instanceNames[instance.Metadata.Name]; exists {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_IDENTIFIER_DUPLICATE", Explanation: fmt.Sprintf("Stack Instance identifier %q is declared by both %q and %q.", instance.Metadata.Name, first, reference), Remedy: "Give every Stack Instance within the Site a unique name."})
		} else {
			instanceNames[instance.Metadata.Name] = reference
			instances = append(instances, instance)
		}
	}
	validatePlacements(hosts, instances, &report)
	report.Hosts = len(site.Spec.Hosts)
	report.StackInstances = len(site.Spec.StackInstances)
	report.Valid = len(report.Diagnostics) == 0
	return report
}

func knownCapability(capability Capability) bool {
	switch capability {
	case capabilityCompose, capabilityTUN, capabilityNetAdmin:
		return true
	default:
		return false
	}
}

func validatePlacements(hosts map[HostName]Host, instances []StackInstance, report *Report) {
	for _, instance := range instances {
		host, exists := hosts[instance.Spec.Host]
		if !exists {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_HOST_REFERENCE_MISSING", Explanation: fmt.Sprintf("Stack Instance %q references missing Host %q.", instance.Metadata.Name, instance.Spec.Host), Remedy: "Reference a Host explicitly listed by the Site."})
			continue
		}
		if instance.Spec.Stack != mediaStack || instance.Spec.Media == nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_STACK_UNSUPPORTED", Explanation: fmt.Sprintf("Stack Instance %q selects unsupported Stack %q.", instance.Metadata.Name, instance.Spec.Stack), Remedy: "Select the media Stack and provide its typed inputs."})
			continue
		}
		has := map[Capability]bool{}
		for _, capability := range host.Spec.Capabilities {
			has[capability] = true
		}
		for _, required := range []Capability{capabilityCompose, capabilityTUN, capabilityNetAdmin} {
			if !has[required] {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_CAPABILITY_MISSING", Explanation: fmt.Sprintf("Host %q lacks capability %q required by Stack Instance %q.", host.Metadata.Name, required, instance.Metadata.Name), Remedy: "Declare a capable Host or move the Stack Instance to one."})
			}
		}
		for _, environment := range []string{"production", "staging"} {
			declared := config.MediaStack{APIVersion: "homelab.media-stack/v1alpha1", Kind: "MediaStack", Spec: config.MediaStackSpec{Defaults: instance.Spec.Media.Defaults, Environments: instance.Spec.Media.Environments}}
			if err := declared.ValidateBackupEnvironment(environment); err != nil {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_STACK_INPUT_INVALID", Explanation: fmt.Sprintf("Stack Instance %q has invalid %s Environment inputs: %v", instance.Metadata.Name, environment, err), Remedy: "Correct the typed Media Stack inputs."})
				break
			}
		}
	}
	validateHostConflicts(instances, report)
}

type claimedResource struct {
	instance string
	claim    storageRootClaim
}

func validateHostConflicts(instances []StackInstance, report *Report) {
	runtimeIdentities := map[HostName]map[RuntimeIdentity]string{}
	ports := map[HostName]map[int]string{}
	storage := map[HostName][]claimedResource{}
	for _, instance := range instances {
		if instance.Spec.Media == nil {
			continue
		}
		host := instance.Spec.Host
		if runtimeIdentities[host] == nil {
			runtimeIdentities[host] = map[RuntimeIdentity]string{}
		}
		if first, exists := runtimeIdentities[host][instance.Spec.RuntimeIdentity]; instance.Spec.RuntimeIdentity != "" && exists {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_RUNTIME_IDENTITY_CONFLICT", Explanation: fmt.Sprintf("Stack Instances %q and %q use runtime identity %q on Host %q.", first, instance.Metadata.Name, instance.Spec.RuntimeIdentity, host), Remedy: "Use a unique runtimeIdentity for each Stack Instance on a Host."})
		} else if instance.Spec.RuntimeIdentity == "" {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_RUNTIME_IDENTITY_INVALID", Explanation: fmt.Sprintf("Stack Instance %q has no runtime identity.", instance.Metadata.Name), Remedy: "Declare a stable runtimeIdentity."})
		} else {
			runtimeIdentities[host][instance.Spec.RuntimeIdentity] = string(instance.Metadata.Name)
		}
		if ports[host] == nil {
			ports[host] = map[int]string{}
		}
		for _, environmentName := range []string{"production", "staging"} {
			environment, exists := instance.Spec.Media.Environments[environmentName]
			if !exists {
				continue
			}
			for _, port := range orderedPorts(environment.Ports) {
				owner := string(instance.Metadata.Name) + "/" + environmentName + "/" + port.name
				if first, exists := ports[host][port.value]; port.value > 0 && exists {
					report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_PORT_CONFLICT", Explanation: fmt.Sprintf("%s and %s both publish port %d on Host %q.", first, owner, port.value, host), Remedy: "Assign distinct published ports to resources placed on the same Host."})
				} else if port.value > 0 {
					ports[host][port.value] = owner
				}
			}
			references := instance.Spec.Media.SensitiveReferences.Environments[environmentName]
			dataClaim := storageClaim(environment.DataRoot)
			backupClaim := storageClaim(environment.BackupRoot)
			if references.DataRoot != "" {
				dataClaim = declaredStorageClaim(references.DataRootClaim, references.DataRootAncestorClaims)
				if dataClaim.exact == "" {
					report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_STORAGE_CLAIM_MISSING", Explanation: fmt.Sprintf("Stack Instance %q has a sensitive data root without a static storage claim.", instance.Metadata.Name), Remedy: "Declare a non-sensitive absolute dataRootClaim for collision validation."})
				}
			}
			if references.BackupRoot != "" {
				backupClaim = declaredStorageClaim(references.BackupRootClaim, references.BackupRootAncestorClaims)
				if backupClaim.exact == "" {
					report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_STORAGE_CLAIM_MISSING", Explanation: fmt.Sprintf("Stack Instance %q has a sensitive backup root without a static storage claim.", instance.Metadata.Name), Remedy: "Declare a non-sensitive absolute backupRootClaim for collision validation."})
				}
			}
			for _, claim := range []storageRootClaim{dataClaim, backupClaim} {
				if claim.exact == "" {
					continue
				}
				for _, claimed := range storage[host] {
					if claimsOverlap(claim, claimed.claim) {
						report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "SITE_STORAGE_CONFLICT", Explanation: fmt.Sprintf("Stack Instances %q and %q have overlapping storage claims on Host %q.", claimed.instance, instance.Metadata.Name, host), Remedy: "Use non-overlapping data and backup roots for Stack Instances on the same Host."})
					}
				}
				storage[host] = append(storage[host], claimedResource{instance: string(instance.Metadata.Name), claim: claim})
			}
		}
	}
}

func encryptedDocumentHasRecipients(path string, recipients ...string) bool {
	contents, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var envelope struct {
		SOPS struct {
			Age []struct {
				Recipient string `yaml:"recipient"`
			} `yaml:"age"`
		} `yaml:"sops"`
	}
	if err := yaml.Unmarshal(contents, &envelope); err != nil {
		return false
	}
	present := map[string]bool{}
	for _, entry := range envelope.SOPS.Age {
		present[entry.Recipient] = true
	}
	for _, recipient := range recipients {
		if !present[recipient] {
			return false
		}
	}
	return true
}

type namedPort struct {
	name  string
	value int
}

func orderedPorts(ports config.Ports) []namedPort {
	return []namedPort{
		{name: "qbittorrent", value: ports.QBittorrent},
		{name: "prowlarr", value: ports.Prowlarr},
		{name: "sonarr", value: ports.Sonarr},
		{name: "radarr", value: ports.Radarr},
		{name: "profilarr", value: ports.Profilarr},
		{name: "jellyfin", value: ports.Jellyfin},
		{name: "seerr", value: ports.Seerr},
	}
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, candidate string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func resolveReference(siteDirectory, reference string) (string, *Diagnostic) {
	if reference == "" || filepath.IsAbs(reference) {
		diagnostic := Diagnostic{Code: "SITE_REFERENCE_ESCAPE", Explanation: fmt.Sprintf("reference %q is not a relative path within the Site.", reference), Remedy: "Use a relative reference that remains inside the selected Site directory."}
		return "", &diagnostic
	}
	siteRoot, err := filepath.EvalSymlinks(siteDirectory)
	if err != nil {
		diagnostic := Diagnostic{Code: "SITE_REFERENCE_MISSING", Explanation: fmt.Sprintf("cannot resolve Site directory: %v", err), Remedy: "Create the selected Site directory and its entry point."}
		return "", &diagnostic
	}
	candidate := filepath.Clean(filepath.Join(siteDirectory, filepath.FromSlash(reference)))
	if !pathWithin(siteDirectory, candidate) {
		diagnostic := Diagnostic{Code: "SITE_REFERENCE_ESCAPE", Explanation: fmt.Sprintf("reference %q escapes the selected Site.", reference), Remedy: "Keep every explicit document reference inside the selected Site directory."}
		return "", &diagnostic
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			diagnostic := Diagnostic{Code: "SITE_REFERENCE_MISSING", Explanation: fmt.Sprintf("referenced document %q does not exist.", reference), Remedy: "Create the document or remove the explicit reference."}
			return "", &diagnostic
		}
		diagnostic := Diagnostic{Code: "SITE_REFERENCE_INVALID", Explanation: fmt.Sprintf("cannot resolve reference %q: %v", reference, err), Remedy: "Fix the explicit document reference."}
		return "", &diagnostic
	}
	if !pathWithin(siteRoot, resolved) {
		diagnostic := Diagnostic{Code: "SITE_REFERENCE_ESCAPE", Explanation: fmt.Sprintf("reference %q resolves outside the selected Site.", reference), Remedy: "Remove the escaping symlink and keep the document inside the Site directory."}
		return "", &diagnostic
	}
	return resolved, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func documentError(kind, reference string, err error) Diagnostic {
	if strings.Contains(err.Error(), "field ") && strings.Contains(err.Error(), " not found") {
		return Diagnostic{Code: "SITE_DOCUMENT_UNKNOWN_FIELD", Explanation: fmt.Sprintf("%s reference %q contains an unknown field.", kind, reference), Remedy: "Correct the field name or remove the unsupported field."}
	}
	if os.IsNotExist(err) {
		return Diagnostic{Code: "SITE_REFERENCE_MISSING", Explanation: fmt.Sprintf("referenced %s document %q does not exist.", kind, reference), Remedy: "Create the document or remove the explicit reference."}
	}
	return Diagnostic{Code: "SITE_DOCUMENT_INVALID", Explanation: fmt.Sprintf("cannot load %s reference %q: %v", kind, reference, err), Remedy: "Fix the referenced document."}
}

func decodeStrict(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	return decoder.Decode(destination)
}
