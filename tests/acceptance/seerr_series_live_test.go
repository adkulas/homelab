package acceptance_test

import "testing"

func TestDisposableStagingSeerrSeriesRequestReachesPlayback(t *testing.T) {
	verifyDisposableStagingSeerrRequest(t,
		"MEDIA_STACK_LIVE_SERIES_REQUEST",
		"MEDIA_STACK_LIVE_SERIES_REQUEST_CONFIG",
		"legal-series.yaml",
		"--legal-series-fixture",
		[]string{"VERIFY_SERIES_REQUESTED", "VERIFY_SERIES_EPISODE_ACQUIRED", "VERIFY_SERIES_EPISODE_HARDLINKED", "VERIFY_SERIES_EPISODE_PLAYBACK_READY"},
	)
}
