package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"

	"github.com/adkulas/homelab/internal/config"
	"gopkg.in/yaml.v3"
)

func existingSiteVPN(ctx context.Context, environment config.Environment, answers *initAnswers) error {
	if environment.SecretScope == nil {
		return nil
	}
	if _, err := os.Stat(environment.VPNSecretsFile); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	credentials, err := decryptSiteVPN(ctx, environment.VPNSecretsFile, environment.SecretScope.Site)
	if err != nil {
		return err
	}
	// The existing Site document is authoritative; init never rotates it.
	answers.ServiceUsername, answers.ServicePassword = credentials.Username, credentials.Password
	return nil
}

func encryptCredentialDocument(ctx context.Context, recipient string, document any) ([]byte, error) {
	plain, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode credential document")
	}
	command := exec.CommandContext(ctx, "sops", "encrypt", "--age", recipient, "--input-type", "yaml", "--output-type", "yaml", "/dev/stdin")
	command.Stdin = bytes.NewReader(plain)
	encrypted, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("encrypt credential document with SOPS")
	}
	return encrypted, nil
}

func encryptScopedCredentials(ctx context.Context, destination string, environment config.Environment, document environmentSecretDocument, recipient string) error {
	vpn := siteVPNSecretDocument{Site: environment.SecretScope.Site, NordVPN: document.NordVPN}
	document.Scope = environment.SecretScope
	document.NordVPN = nordVPNSecrets{}
	encrypted, err := encryptCredentialDocument(ctx, recipient, document)
	if err != nil {
		return err
	}
	if _, err := os.Stat(environment.VPNSecretsFile); os.IsNotExist(err) {
		encryptedVPN, err := encryptCredentialDocument(ctx, recipient, vpn)
		if err != nil {
			return err
		}
		if err := writeSecretExclusive(environment.VPNSecretsFile, encryptedVPN); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := decryptSiteVPN(ctx, environment.VPNSecretsFile, environment.SecretScope.Site); err != nil {
		return err
	}
	return writeSecretExclusive(destination, encrypted)
}

// Link a fully written private file into place without replacing another writer's
// credentials. Only ciphertext ever touches disk.
func writeSecretExclusive(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary.Name(), path); err != nil {
		return fmt.Errorf("publish credential document without replacing existing credentials: %w", err)
	}
	return nil
}

func validateScopedInitPolicy(declared config.MediaStack, answers initAnswers) error {
	vpn := declared.Spec.Acquisition.VPN
	categories := []string(nil)
	if answers.ServerCategory != "" {
		categories = []string{answers.ServerCategory}
	}
	declaredInterval, _ := time.ParseDuration(vpn.CatalogueUpdateInterval)
	answerInterval, _ := time.ParseDuration(answers.CatalogueUpdateInterval)
	if vpn.Provider != "nordvpn" || vpn.Protocol != "openvpn" || vpn.OpenVPNProtocol != answers.OpenVPNProtocol || !reflect.DeepEqual(vpn.Server.Countries, []string{answers.Country}) || !reflect.DeepEqual(vpn.Server.Categories, categories) || declaredInterval != answerInterval {
		return fmt.Errorf("initialization answers cannot change reusable acquisition policy; edit services/media/media-stack.yaml first")
	}
	return nil
}
