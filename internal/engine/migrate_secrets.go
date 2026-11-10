package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

type credentialMigration struct {
	path      string
	document  any
	recipient string
}

// MigrateSecrets preserves credential values and original age recipients. It
// validates all destinations before publishing, never replaces an existing file,
// and leaves the legacy documents available until the operator completes cutover.
func MigrateSecrets(ctx context.Context, deployment *Deployment, legacyPath string) (int, error) {
	if deployment == nil {
		return 0, fmt.Errorf("migration requires a selected Site and Stack Instance")
	}
	legacy, err := config.Load(legacyPath)
	if err != nil {
		return 0, err
	}
	var pending []credentialMigration
	var sharedVPN *siteVPNSecretDocument
	var vpnPath string
	vpnRecipients := map[string]bool{}
	count := 0
	for _, name := range []string{"production", "staging"} {
		source, exists := legacy.Spec.Environments[name]
		if !exists {
			continue
		}
		path := selectedSecretPath(legacyPath, source)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return 0, err
		}
		var document environmentSecretDocument
		if err := readSecretDocument(ctx, path, &document); err != nil {
			return 0, err
		}
		if document.Scope != nil {
			return 0, fmt.Errorf("legacy source is already owned; refusing implicit reassignment")
		}
		if _, err := environmentSecretsFromDocument(document); err != nil {
			return 0, err
		}
		destination, exists := deployment.Declared.Spec.Environments[name]
		if !exists || destination.SecretScope == nil {
			return 0, fmt.Errorf("migration destination requires declared credential ownership")
		}
		recipient, err := credentialRecipients(path)
		if err != nil {
			return 0, err
		}
		vpn := siteVPNSecretDocument{Site: destination.SecretScope.Site, NordVPN: document.NordVPN}
		if sharedVPN != nil && *sharedVPN != vpn {
			return 0, fmt.Errorf("legacy Environments have different NordVPN credentials; choose their Site ownership explicitly before migration")
		}
		sharedVPN, vpnPath = &vpn, destination.VPNSecretsFile
		for _, value := range strings.Split(recipient, ",") {
			vpnRecipients[value] = true
		}
		document.NordVPN = nordVPNSecrets{}
		document.Scope = destination.SecretScope
		pending = append(pending, credentialMigration{destination.SecretsFile, document, recipient})
		count++
	}
	if count == 0 {
		return 0, fmt.Errorf("no legacy credential documents found")
	}
	var orderedRecipients []string
	for recipient := range vpnRecipients {
		orderedRecipients = append(orderedRecipients, recipient)
	}
	sort.Strings(orderedRecipients)
	pending = append(pending, credentialMigration{vpnPath, *sharedVPN, strings.Join(orderedRecipients, ",")})
	var publish []credentialMigration
	for _, item := range pending {
		if _, err := os.Stat(item.path); os.IsNotExist(err) {
			publish = append(publish, item)
			continue
		} else if err != nil {
			return 0, err
		}
		var current any
		switch item.document.(type) {
		case environmentSecretDocument:
			current = &environmentSecretDocument{}
		case siteVPNSecretDocument:
			current = &siteVPNSecretDocument{}
		}
		if err := readSecretDocument(ctx, item.path, current); err != nil {
			return 0, fmt.Errorf("validate existing migration destination: %w", err)
		}
		if !reflect.DeepEqual(reflect.ValueOf(current).Elem().Interface(), item.document) {
			return 0, fmt.Errorf("existing migration destination has different ownership or credentials; refusing replacement")
		}
	}
	// Complete encryption before any destination is created.
	encrypted := make([][]byte, len(publish))
	for index, item := range publish {
		encrypted[index], err = encryptCredentialDocument(ctx, item.recipient, item.document)
		if err != nil {
			return 0, err
		}
	}
	for index, item := range publish {
		if err := writeSecretExclusive(item.path, encrypted[index]); err != nil {
			return 0, err
		}
	}
	// Verify every published document through the same reader used by apply.
	for _, name := range []string{"production", "staging"} {
		environment := deployment.Declared.Spec.Environments[name]
		if _, err := os.Stat(environment.SecretsFile); os.IsNotExist(err) {
			continue
		}
		if _, err := decryptSelectedEnvironmentSecrets(ctx, deployment.ConfigPath, environment); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func credentialRecipients(path string) (string, error) {
	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	var envelope struct {
		SOPS struct {
			Age []struct {
				Recipient string `yaml:"recipient"`
			} `yaml:"age"`
		} `yaml:"sops"`
	}
	if err := yaml.Unmarshal(contents, &envelope); err != nil {
		return "", fmt.Errorf("invalid legacy SOPS envelope")
	}
	var recipients []string
	for _, age := range envelope.SOPS.Age {
		if age.Recipient != "" {
			recipients = append(recipients, age.Recipient)
		}
	}
	if len(recipients) == 0 {
		return "", fmt.Errorf("legacy credential migration requires age recipients")
	}
	return strings.Join(recipients, ","), nil
}
