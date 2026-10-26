package acceptance_test

import (
	"os/exec"
	"testing"
)

func TestNetworkBackupCommands(t *testing.T) {
	command := exec.Command("python3", "-B", "-m", "unittest", "discover", "-s", "services/network/backup", "-p", "test_*.py")
	command.Dir = repositoryRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("network backup CLI acceptance checks: %v\n%s", err, output)
	}
}
