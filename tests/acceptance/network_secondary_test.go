package acceptance_test

import (
	"os/exec"
	"testing"
)

func TestNetworkSecondaryDeployment(t *testing.T) {
	command := exec.Command("python3", "-B", "-m", "unittest", "discover", "-s", "services/network/secondary", "-p", "test_*.py")
	command.Dir = repositoryRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("secondary DNS deployment checks: %v\n%s", err, output)
	}
}
