package acceptance_test

import (
	"os/exec"
	"testing"
)

func TestNetworkDNSCommands(t *testing.T) {
	command := exec.Command("python3", "-B", "-m", "unittest", "discover", "-s", "services/network/dns", "-p", "test_*.py")
	command.Dir = repositoryRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("declared DNS CLI acceptance checks: %v\n%s", err, output)
	}
}
