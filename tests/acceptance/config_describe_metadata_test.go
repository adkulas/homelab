package acceptance_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestConfigDescribeClassifiesGeneratedAPIKeysWithoutClaimingSOPSOwnership(t *testing.T) {
	for _, serviceName := range []string{"prowlarr", "radarr", "sonarr"} {
		t.Run(serviceName, func(t *testing.T) {
			document := describeContract(t, serviceName)
			setting := findContractSetting(t, document, "apiKey")
			if setting.Control != "derived" || !setting.Sensitive {
				t.Fatalf("generated API key classification = %#v", setting)
			}
			if strings.Contains(strings.ToLower(setting.Source+setting.OperatorChange), "sops") {
				t.Fatalf("generated API key falsely claims SOPS ownership: %#v", setting)
			}
		})
	}
}

func TestConfigDescribeReportsActualDeclaredLifecycles(t *testing.T) {
	tests := []struct {
		service, id string
		want        []string
	}{
		{"sonarr", "backup.retention.daily", []string{"preserve"}},
		{"prowlarr", "publicTorrentSources", []string{"reconcile"}},
		{"jellyfin", "hardwareTranscoding.preference", []string{"render", "initialize", "verify"}},
		{"radarr", "policy.pcdRevision", []string{"synchronize", "verify"}},
	}
	for _, test := range tests {
		document := describeContract(t, test.service)
		setting := findContractSetting(t, document, test.id)
		if !reflect.DeepEqual(setting.Lifecycle, test.want) {
			t.Errorf("%s.%s lifecycle = %#v, want %#v", test.service, test.id, setting.Lifecycle, test.want)
		}
	}
}

func TestConfigDescribeSeparatesFixedPolicySourceFromValue(t *testing.T) {
	document := describeContract(t, "qbittorrent")
	setting := findContractSetting(t, document, "seeding.ratioLimit")
	if setting.Source != "Stack Policy#seeding.ratioLimit" || setting.Type != "number" || setting.Default != float64(1) {
		t.Fatalf("fixed policy metadata = %#v", setting)
	}
}

func describeContract(t *testing.T, service string) contractOutput {
	t.Helper()
	output, err := configDescribeCommand(t, "--service", service, "--output", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("media-stack config describe failed: %v\n%s", err, output)
	}
	var document contractOutput
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode configuration contract: %v", err)
	}
	return document
}

func findContractSetting(t *testing.T, document contractOutput, id string) contractSetting {
	t.Helper()
	for _, setting := range document.Services[0].Settings {
		if setting.ID == id {
			return setting
		}
	}
	t.Fatalf("setting %q is absent", id)
	return contractSetting{}
}
