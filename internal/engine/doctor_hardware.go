package engine

import (
	"context"
	"strconv"

	"github.com/adkulas/homelab/internal/hardware"
)

type hardwareProbe func(hardware.Transcoding) bool

func doctorHardwareTranscodingDiagnostic(ctx context.Context, environment, preference, jellyfinImage string) Diagnostic {
	detection := hardware.Transcoding{Status: hardware.StatusMissing}
	if preference == "auto" {
		detection = hardware.DetectTranscoding()
	}
	return hardwareTranscodingDiagnostic(environment, preference, detection, func(detected hardware.Transcoding) bool {
		return runDockerProbeQuiet(ctx,
			"--device", detected.RenderDevice+":"+detected.RenderDevice,
			"--group-add", strconv.Itoa(detected.GroupID),
			"--entrypoint", "/usr/lib/jellyfin-ffmpeg/vainfo",
			jellyfinImage,
			"--display", "drm", "--device", detected.RenderDevice,
		)
	})
}

func hardwareTranscodingDiagnostic(environment, preference string, detection hardware.Transcoding, probe hardwareProbe) Diagnostic {
	diagnostic := Diagnostic{
		Environment: environment,
		Subject:     "optional Jellyfin hardware transcoding",
		Severity:    "info",
	}
	if preference == "disabled" {
		diagnostic.Code = "PREFLIGHT_HARDWARE_TRANSCODING_DISABLED"
		diagnostic.Status = "skip"
		diagnostic.Explanation = "hardware transcoding is disabled for the selected Environment"
		diagnostic.Remedy = "Set hardwareTranscoding to auto to detect a supported Linux DRM render node."
		return diagnostic
	}
	if detection.Status == hardware.StatusMissing {
		diagnostic.Code = "PREFLIGHT_HARDWARE_TRANSCODING_UNAVAILABLE"
		diagnostic.Status = "skip"
		diagnostic.Explanation = "/dev/dri/renderD128 is absent; plan will retain the portable topology"
		diagnostic.Remedy = "No action is required, or expose a supported Intel or AMD DRM render node to enable acceleration."
		return diagnostic
	}
	if detection.Status != hardware.StatusSupported || !probe(detection) {
		diagnostic.Code = "PREFLIGHT_HARDWARE_TRANSCODING_UNUSABLE"
		diagnostic.Status = "fail"
		diagnostic.Severity = "error"
		diagnostic.Explanation = "the detected DRM render node is not a usable character device in the pinned Jellyfin image"
		diagnostic.Remedy = "Fix host render-device permissions and drivers, or set hardwareTranscoding to disabled."
		diagnostic.Retryable = true
		return diagnostic
	}
	diagnostic.Code = "PREFLIGHT_HARDWARE_TRANSCODING_SUPPORTED"
	diagnostic.Status = "pass"
	diagnostic.Explanation = "the pinned Jellyfin image can query the detected DRM render node"
	return diagnostic
}
