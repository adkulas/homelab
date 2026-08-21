package hardware

import (
	"errors"
	"io/fs"
	"testing"
)

func TestDetectTranscodingFixtures(t *testing.T) {
	tests := []struct {
		name   string
		lookup renderDeviceLookup
		want   Transcoding
	}{
		{
			name: "supported render device",
			lookup: func(string) (renderDevice, error) {
				return renderDevice{mode: fs.ModeDevice | fs.ModeCharDevice, groupID: 109}, nil
			},
			want: Transcoding{Status: StatusSupported, RenderDevice: defaultRenderDevice, GroupID: 109},
		},
		{
			name: "missing render device",
			lookup: func(string) (renderDevice, error) {
				return renderDevice{}, fs.ErrNotExist
			},
			want: Transcoding{Status: StatusMissing},
		},
		{
			name: "path is not a character device",
			lookup: func(string) (renderDevice, error) {
				return renderDevice{mode: 0o644, groupID: 109}, nil
			},
			want: Transcoding{Status: StatusUnusable},
		},
		{
			name: "device metadata cannot be read",
			lookup: func(string) (renderDevice, error) {
				return renderDevice{}, errors.New("permission denied")
			},
			want: Transcoding{Status: StatusUnusable},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := detectTranscoding(test.lookup); got != test.want {
				t.Fatalf("detectTranscoding() = %#v, want %#v", got, test.want)
			}
		})
	}
}
