package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDoctorReportsDisabledHardwareTranscodingAsSkipped(t *testing.T) {
	configPath, binDirectory := doctorFixture(t)
	configuration := strings.ReplaceAll(string(readFile(t, configPath)), "hardwareTranscoding: auto", "hardwareTranscoding: disabled")
	writeFile(t, configPath, []byte(configuration), 0o600)
	command := exec.Command("go", "run", "./cmd/media-stack", "doctor", "--environment", "staging", "--config", configPath, "--output", "json")
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, output)
	}
	var report DoctorReportFixture
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "PREFLIGHT_HARDWARE_TRANSCODING_DISABLED" && diagnostic.Status == "skip" {
			return
		}
	}
	t.Fatalf("doctor omitted disabled hardware diagnostic: %s", output)
}

type DoctorReportFixture struct {
	Diagnostics []struct {
		Code   string `json:"code"`
		Status string `json:"status"`
	} `json:"diagnostics"`
}
