package config

import "testing"

func TestJellyfinRecoveryScanLocalTime(t *testing.T) {
	for _, test := range []struct {
		name, configured, environment string
		want                          int
		invalid                       bool
	}{
		{name: "Production default", environment: "production", want: 180},
		{name: "Staging default", environment: "staging", want: 240},
		{name: "Production override", configured: "23:27", environment: "production", want: 1407},
		{name: "Staging rollover", configured: "23:27", environment: "staging", want: 27},
		{name: "invalid hour", configured: "24:00", environment: "production", invalid: true},
		{name: "missing leading zero", configured: "3:00", environment: "production", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			defaults := Defaults{JellyfinRecoveryScanTime: test.configured}
			got, err := defaults.JellyfinRecoveryScanMinutes(test.environment)
			if (err != nil) != test.invalid || got != test.want {
				t.Fatalf("local recovery scan minutes = %d, error = %v, want %d with invalid=%t", got, err, test.want, test.invalid)
			}
		})
	}
}
