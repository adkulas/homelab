package config

import (
	"strings"
	"testing"
)

func TestValidateEnvironmentHardwareTranscodingPreference(t *testing.T) {
	for _, preference := range []HardwareTranscodingPreference{HardwareTranscodingAuto, HardwareTranscodingDisabled} {
		t.Run(string(preference), func(t *testing.T) {
			declared := hardwarePreferenceFixture(preference)
			if err := declared.ValidateEnvironment("staging"); err != nil {
				t.Fatalf("ValidateEnvironment() error = %v", err)
			}
		})
	}

	declared := hardwarePreferenceFixture(HardwareTranscodingPreference("required"))
	err := declared.ValidateEnvironment("staging")
	if err == nil || !strings.Contains(err.Error(), "hardwareTranscoding") {
		t.Fatalf("ValidateEnvironment() error = %v, want hardwareTranscoding explanation", err)
	}
}

func hardwarePreferenceFixture(preference HardwareTranscodingPreference) MediaStack {
	return MediaStack{Spec: MediaStackSpec{Environments: map[string]Environment{
		"production": {DataRoot: "/srv/media/production", HardwareTranscoding: preference},
		"staging":    {DataRoot: "/srv/media/staging", HardwareTranscoding: preference},
	}}}
}
