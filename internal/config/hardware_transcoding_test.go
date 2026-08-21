package config

import (
	"strings"
	"testing"
)

func TestValidateEnvironmentHardwareTranscodingPreference(t *testing.T) {
	for _, preference := range []string{"auto", "disabled"} {
		t.Run(preference, func(t *testing.T) {
			declared := hardwarePreferenceFixture(preference)
			if err := declared.ValidateEnvironment("staging"); err != nil {
				t.Fatalf("ValidateEnvironment() error = %v", err)
			}
		})
	}

	declared := hardwarePreferenceFixture("required")
	err := declared.ValidateEnvironment("staging")
	if err == nil || !strings.Contains(err.Error(), "hardwareTranscoding") {
		t.Fatalf("ValidateEnvironment() error = %v, want hardwareTranscoding explanation", err)
	}
}

func hardwarePreferenceFixture(preference string) MediaStack {
	return MediaStack{Spec: MediaStackSpec{Environments: map[string]Environment{
		"production": {DataRoot: "/srv/media/production", HardwareTranscoding: preference},
		"staging":    {DataRoot: "/srv/media/staging", HardwareTranscoding: preference},
	}}}
}
