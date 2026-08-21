package engine

import (
	"testing"

	"github.com/adkulas/homelab/internal/hardware"
)

func TestHardwareTranscodingDiagnosticFixtures(t *testing.T) {
	tests := []struct {
		name       string
		preference string
		detection  hardware.Transcoding
		probe      bool
		wantCode   string
		wantStatus string
	}{
		{name: "disabled", preference: "disabled", wantCode: "PREFLIGHT_HARDWARE_TRANSCODING_DISABLED", wantStatus: "skip"},
		{name: "missing", preference: "auto", detection: hardware.Transcoding{Status: hardware.StatusMissing}, wantCode: "PREFLIGHT_HARDWARE_TRANSCODING_UNAVAILABLE", wantStatus: "skip"},
		{name: "unusable", preference: "auto", detection: hardware.Transcoding{Status: hardware.StatusUnusable}, wantCode: "PREFLIGHT_HARDWARE_TRANSCODING_UNUSABLE", wantStatus: "fail"},
		{name: "supported", preference: "auto", detection: hardware.Transcoding{Status: hardware.StatusSupported, RenderDevice: "/dev/dri/renderD128", GroupID: 109}, probe: true, wantCode: "PREFLIGHT_HARDWARE_TRANSCODING_SUPPORTED", wantStatus: "pass"},
		{name: "container cannot use device", preference: "auto", detection: hardware.Transcoding{Status: hardware.StatusSupported, RenderDevice: "/dev/dri/renderD128", GroupID: 109}, wantCode: "PREFLIGHT_HARDWARE_TRANSCODING_UNUSABLE", wantStatus: "fail"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probeCalls := 0
			diagnostic := hardwareTranscodingDiagnostic("staging", test.preference, test.detection, func(hardware.Transcoding) bool {
				probeCalls++
				return test.probe
			})
			if diagnostic.Code != test.wantCode || diagnostic.Status != test.wantStatus {
				t.Fatalf("diagnostic = %#v, want code %q status %q", diagnostic, test.wantCode, test.wantStatus)
			}
			if test.detection.Status != hardware.StatusSupported && probeCalls != 0 {
				t.Fatalf("container probe calls = %d, want 0", probeCalls)
			}
		})
	}
}
