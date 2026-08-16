package config

import (
	"fmt"
	"os"
	"regexp"

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
}

type Defaults struct {
	Timezone   string `yaml:"timezone"`
	RuntimeUID int    `yaml:"runtimeUID"`
	RuntimeGID int    `yaml:"runtimeGID"`
}

type Environment struct{}

type Versions struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Images     map[string]string `yaml:"images"`
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
	return declared, nil
}

func (declared MediaStack) ValidateEnvironment(name string) error {
	if name != "production" && name != "staging" {
		return fmt.Errorf("environment %q is not production or staging", name)
	}
	if _, exists := declared.Spec.Environments[name]; !exists {
		return fmt.Errorf("environment %q is not declared", name)
	}
	return nil
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
