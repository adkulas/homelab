package config

import (
	"fmt"
	"time"
)

const (
	DefaultJellyfinRecoveryScanTime          = "03:00"
	StagingJellyfinRecoveryScanOffsetMinutes = 60
)

// JellyfinRecoveryScanMinutes resolves the daily scan's local wall-clock time.
func (defaults Defaults) JellyfinRecoveryScanMinutes(environment string) (int, error) {
	const format = "15:04"
	configured := defaults.JellyfinRecoveryScanTime
	if configured == "" {
		configured = DefaultJellyfinRecoveryScanTime
	}
	localTime, err := time.Parse(format, configured)
	if err != nil || localTime.Format(format) != configured {
		return 0, fmt.Errorf("defaults.jellyfinRecoveryScanTime must be a local time in HH:MM format")
	}
	minutes := localTime.Hour()*60 + localTime.Minute()
	if environment == "staging" {
		minutes = (minutes + StagingJellyfinRecoveryScanOffsetMinutes) % (24 * 60)
	}
	return minutes, nil
}
