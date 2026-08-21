package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const schemaVersion = "homelab.media-stack/v1alpha1"

type MediaStack struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Spec       MediaStackSpec `yaml:"spec"`
}

type MediaStackSpec struct {
	Defaults     Defaults               `yaml:"defaults"`
	Environments map[string]Environment `yaml:"environments"`
	Acquisition  Acquisition            `yaml:"acquisition,omitempty"`
}

type Defaults struct {
	Timezone       string `yaml:"timezone"`
	RuntimeUID     int    `yaml:"runtimeUID"`
	RuntimeGID     int    `yaml:"runtimeGID"`
	LANBindAddress string `yaml:"lanBindAddress"`
}

// HardwareTranscodingPreference controls whether an Environment detects a hardware overlay.
type HardwareTranscodingPreference string

const (
	HardwareTranscodingAuto     HardwareTranscodingPreference = "auto"
	HardwareTranscodingDisabled HardwareTranscodingPreference = "disabled"
)

// Valid reports whether the preference is part of the declared configuration contract.
func (preference HardwareTranscodingPreference) Valid() bool {
	return preference == HardwareTranscodingAuto || preference == HardwareTranscodingDisabled
}

type Environment struct {
	ProjectName         string                        `yaml:"projectName"`
	DataRoot            string                        `yaml:"dataRoot"`
	SecretsFile         string                        `yaml:"secretsFile"`
	HardwareTranscoding HardwareTranscodingPreference `yaml:"hardwareTranscoding"`
	Ports               Ports                         `yaml:"ports"`
}

type Acquisition struct {
	VPN                  VPN                   `yaml:"vpn"`
	PublicTorrentSources []PublicTorrentSource `yaml:"publicTorrentSources,omitempty"`
}

type PublicTorrentSource struct {
	ID      string `yaml:"id"`
	Enabled bool   `yaml:"enabled"`
}

type VPN struct {
	Provider                string `yaml:"provider"`
	Protocol                string `yaml:"protocol"`
	OpenVPNProtocol         string `yaml:"openvpnProtocol"`
	Server                  Server `yaml:"server"`
	CatalogueUpdateInterval string `yaml:"catalogueUpdateInterval"`
}

type Server struct {
	Countries  []string `yaml:"countries"`
	Categories []string `yaml:"categories,omitempty"`
}

type Ports struct {
	QBittorrent int `yaml:"qbittorrent"`
	Prowlarr    int `yaml:"prowlarr"`
	Sonarr      int `yaml:"sonarr"`
	Radarr      int `yaml:"radarr"`
	Profilarr   int `yaml:"profilarr"`
	Jellyfin    int `yaml:"jellyfin"`
	Seerr       int `yaml:"seerr"`
}

type Versions struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Images     map[string]string `yaml:"images"`
	Policy     Policy            `yaml:"policy"`
}

type Policy struct {
	ProfilarrPCDRevision string `yaml:"profilarrPcdRevision"`
}

var digestReference = regexp.MustCompile(`^[^[:space:]@]+@sha256:[a-f0-9]{64}$`)

func Load(path string) (MediaStack, error) {
	var declared MediaStack
	if err := decodeStrict(path, &declared); err != nil {
		return MediaStack{}, fmt.Errorf("load Declared Configuration: %w", err)
	}
	if declared.APIVersion != schemaVersion || declared.Kind != "MediaStack" {
		return MediaStack{}, fmt.Errorf("load Declared Configuration: unsupported document %q %q", declared.APIVersion, declared.Kind)
	}
	seenSources := map[string]bool{}
	for _, source := range declared.Spec.Acquisition.PublicTorrentSources {
		if source.ID != "internetarchive" {
			return MediaStack{}, fmt.Errorf("load Declared Configuration: Public Torrent Source %q is not in the approved catalog", source.ID)
		}
		if seenSources[source.ID] {
			return MediaStack{}, fmt.Errorf("load Declared Configuration: Public Torrent Source %q is declared more than once", source.ID)
		}
		seenSources[source.ID] = true
	}
	return declared, nil
}

func Write(path string, declared MediaStack) error {
	contents, err := yaml.Marshal(declared)
	if err != nil {
		return fmt.Errorf("encode Declared Configuration: %w", err)
	}
	return writeAtomic(path, contents, 0o644)
}

func (declared MediaStack) ValidateEnvironment(name string) error {
	return declared.validateEnvironment(name, false)
}

func (declared MediaStack) ValidateInitializableEnvironment(name string) error {
	return declared.validateEnvironment(name, true)
}

func (declared MediaStack) validateEnvironment(name string, allowMissingHardwarePreference bool) error {
	if name != "production" && name != "staging" {
		return fmt.Errorf("environment %q is not production or staging", name)
	}
	if _, exists := declared.Spec.Environments[name]; !exists {
		return fmt.Errorf("environment %q is not declared", name)
	}
	preference := declared.Spec.Environments[name].HardwareTranscoding
	if !preference.Valid() && !(allowMissingHardwarePreference && preference == "") {
		return fmt.Errorf("environment %q hardwareTranscoding must be auto or disabled", name)
	}
	production, productionExists := declared.Spec.Environments["production"]
	staging, stagingExists := declared.Spec.Environments["staging"]
	if !productionExists || !stagingExists {
		return fmt.Errorf("Production and Staging Environments must both be declared")
	}
	if !filepath.IsAbs(production.DataRoot) || !filepath.IsAbs(staging.DataRoot) {
		return fmt.Errorf("Production and Staging data roots must be absolute")
	}
	productionRoot := filepath.Clean(production.DataRoot)
	stagingRoot := filepath.Clean(staging.DataRoot)
	if pathContains(productionRoot, stagingRoot) || pathContains(stagingRoot, productionRoot) {
		return fmt.Errorf("Production and Staging data roots must not overlap: %q and %q", productionRoot, stagingRoot)
	}
	return nil
}

func pathContains(parent, candidate string) bool {
	relative, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func LoadVersions(path string) (Versions, error) {
	var versions Versions
	if err := decodeStrict(path, &versions); err != nil {
		return Versions{}, fmt.Errorf("load checked-in versions: %w", err)
	}
	if versions.APIVersion != schemaVersion || versions.Kind != "MediaStackVersions" {
		return Versions{}, fmt.Errorf("load checked-in versions: unsupported document %q %q", versions.APIVersion, versions.Kind)
	}
	return versions, nil
}

func ValidateImage(name, reference string) error {
	if !digestReference.MatchString(reference) {
		return fmt.Errorf("image %q must be an immutable sha256 digest reference", name)
	}
	return nil
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

func writeAtomic(path string, contents []byte, fallbackMode os.FileMode) error {
	mode := fallbackMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".media-stack-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
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
	return os.Rename(temporaryPath, path)
}
