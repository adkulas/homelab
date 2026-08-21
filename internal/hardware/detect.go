package hardware

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

const defaultRenderDevice = "/dev/dri/renderD128"

// Transcoding describes the host resources that Jellyfin needs for VA-API.
// The zero value means that the portable topology should be rendered.
type Status string

const (
	StatusSupported Status = "supported"
	StatusMissing   Status = "missing"
	StatusUnusable  Status = "unusable"
)

type Transcoding struct {
	Status       Status
	RenderDevice string
	GroupID      int
}

type renderDevice struct {
	mode    fs.FileMode
	groupID int
}

type renderDeviceLookup func(string) (renderDevice, error)

// DetectTranscoding detects the standard Linux DRM render node used by Intel
// and AMD hardware acceleration.
func DetectTranscoding() Transcoding {
	return detectTranscoding(inspectRenderDevice)
}

func detectTranscoding(lookup renderDeviceLookup) Transcoding {
	device, err := lookup(defaultRenderDevice)
	if errors.Is(err, fs.ErrNotExist) {
		return Transcoding{Status: StatusMissing}
	}
	if err != nil || device.mode&fs.ModeDevice == 0 || device.mode&fs.ModeCharDevice == 0 {
		return Transcoding{Status: StatusUnusable}
	}
	return Transcoding{Status: StatusSupported, RenderDevice: defaultRenderDevice, GroupID: device.groupID}
}

func inspectRenderDevice(path string) (renderDevice, error) {
	info, err := os.Stat(path)
	if err != nil {
		return renderDevice{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return renderDevice{}, fs.ErrInvalid
	}
	return renderDevice{mode: info.Mode(), groupID: int(stat.Gid)}, nil
}
