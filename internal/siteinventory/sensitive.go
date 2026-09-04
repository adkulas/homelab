package siteinventory

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

func resolveSensitiveInputs(ctx context.Context, siteDirectory string, declared *SensitiveValuesSpec, media *MediaStackInstance, environment string, stack *config.MediaStack) ([]RedactedReference, error) {
	references := media.SensitiveReferences.Environments[environment]
	if references.DataRoot == "" && references.BackupRoot == "" {
		return nil, nil
	}
	if declared == nil {
		return nil, CodedError{Code: "SITE_SENSITIVE_VALUES_MISSING", Explanation: "the selected Stack Instance uses sensitive references but the Site declares no sensitive-values document.", Remedy: "Declare the Site-owned SOPS document and its daily and recovery recipients."}
	}
	path, diagnostic := resolveReference(siteDirectory, declared.Document)
	if diagnostic != nil {
		return nil, CodedError{Code: diagnostic.Code, Explanation: diagnostic.Explanation, Remedy: diagnostic.Remedy}
	}
	command := exec.CommandContext(ctx, "sops", "decrypt", "--output-type", "yaml", path)
	contents, err := command.Output()
	if err != nil {
		return nil, CodedError{Code: "SITE_SENSITIVE_VALUES_DECRYPT_FAILED", Explanation: "the selected Site-sensitive values could not be decrypted.", Remedy: "Install SOPS and provide an age identity authorized for this Site's daily or recovery recipient."}
	}
	values, err := decodeSensitiveValues(contents)
	if err != nil {
		return nil, err
	}
	environmentConfig, exists := stack.Spec.Environments[environment]
	if !exists {
		return nil, CodedError{Code: "SITE_ENVIRONMENT_MISSING", Explanation: fmt.Sprintf("Environment %q is not declared by the selected Stack Instance.", environment), Remedy: "Select a declared Production or Staging Environment."}
	}
	dataValue, err := requiredSensitiveValue(values, references.DataRoot)
	if err != nil {
		return nil, err
	}
	backupValue, err := requiredSensitiveValue(values, references.BackupRoot)
	if err != nil {
		return nil, err
	}
	if (dataValue != "" && !filepath.IsAbs(dataValue)) || (backupValue != "" && !filepath.IsAbs(backupValue)) {
		return nil, CodedError{Code: "SITE_SENSITIVE_VALUE_INVALID", Explanation: "the selected sensitive storage roots are not absolute paths.", Remedy: "Store absolute data and backup roots in the Site-sensitive values document."}
	}
	if dataValue != "" && !claimsMatch(dataValue, declaredStorageClaim(references.DataRootClaim, references.DataRootAncestorClaims)) {
		return nil, CodedError{Code: "SITE_STORAGE_CLAIM_MISMATCH", Explanation: "the selected sensitive data root does not match its declared exact-path and ancestor claims.", Remedy: "Regenerate the dataRootClaim and dataRootAncestorClaims from the canonical sensitive path."}
	}
	if backupValue != "" && !claimsMatch(backupValue, declaredStorageClaim(references.BackupRootClaim, references.BackupRootAncestorClaims)) {
		return nil, CodedError{Code: "SITE_STORAGE_CLAIM_MISMATCH", Explanation: "the selected sensitive backup root does not match its declared exact-path and ancestor claims.", Remedy: "Regenerate the backupRootClaim and backupRootAncestorClaims from the canonical sensitive path."}
	}
	if dataValue != "" && backupValue != "" && pathsOverlap(dataValue, backupValue) {
		return nil, CodedError{Code: "SITE_STORAGE_CONFLICT", Explanation: "the selected sensitive data and backup roots overlap.", Remedy: "Use non-overlapping storage roots."}
	}
	redacted := []RedactedReference{}
	if dataValue != "" {
		environmentConfig.DataRoot = "/<redacted:" + string(references.DataRoot) + ">"
		redacted = append(redacted, RedactedReference{ID: references.DataRoot, Value: "<redacted>"})
	}
	if backupValue != "" {
		environmentConfig.BackupRoot = "/<redacted:" + string(references.BackupRoot) + ">"
		redacted = append(redacted, RedactedReference{ID: references.BackupRoot, Value: "<redacted>"})
	}
	sort.Slice(redacted, func(first, second int) bool { return redacted[first].ID < redacted[second].ID })
	stack.Spec.Environments[environment] = environmentConfig
	return redacted, nil
}

func decodeSensitiveValues(contents []byte) (map[SensitiveValueID]string, error) {
	var document SiteSensitiveValues
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil || document.APIVersion != siteAPIVersion || document.Kind != sensitiveKind {
		return nil, CodedError{Code: "SITE_SENSITIVE_VALUES_INVALID", Explanation: "the decrypted Site-sensitive values document is invalid or unsupported.", Remedy: "Use apiVersion homelab.site/v1alpha1 and kind SiteSensitiveValues with unique id/value entries."}
	}
	values := map[SensitiveValueID]string{}
	for _, value := range document.Spec.Values {
		if value.ID == "" || value.Value == "" {
			return nil, CodedError{Code: "SITE_SENSITIVE_VALUES_INVALID", Explanation: "a Site-sensitive value has an empty semantic ID or value.", Remedy: "Give every entry a non-empty id and value."}
		}
		if _, exists := values[value.ID]; exists {
			return nil, CodedError{Code: "SITE_SENSITIVE_VALUE_DUPLICATE", Explanation: fmt.Sprintf("Site-sensitive value ID %q is declared more than once.", value.ID), Remedy: "Give every Site-sensitive value a unique semantic ID."}
		}
		values[value.ID] = value.Value
	}
	return values, nil
}

func requiredSensitiveValue(values map[SensitiveValueID]string, id SensitiveValueID) (string, error) {
	if id == "" {
		return "", nil
	}
	value, exists := values[id]
	if !exists {
		return "", CodedError{Code: "SITE_SENSITIVE_VALUE_MISSING", Explanation: fmt.Sprintf("sensitive value %q required by the selected Stack Instance is missing.", id), Remedy: "Add the semantic ID to the selected Site's encrypted values document."}
	}
	return value, nil
}
