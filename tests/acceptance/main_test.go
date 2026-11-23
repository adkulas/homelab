package acceptance_test

import (
	"os"
	"testing"
)

// Fixture deployments must never register or remove services on the test host.
func TestMain(m *testing.M) {
	os.Setenv("SYSTEMD_OFFLINE", "1")
	os.Exit(m.Run())
}
