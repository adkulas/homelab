package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeRunbookChecksHostAndRevisionBeforeApply(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the Media Stack upgrade runbook uses jq")
	}
	readme := string(readFile(t, filepath.Join(repositoryRoot(t), "services", "media", "README.md")))
	_, upgrade, _ := strings.Cut(readme, "## Upgrading image pins\n")
	upgrade, _, _ = strings.Cut(upgrade, "## Quick start\n")
	var targetCheck string
	for _, block := range strings.Split(upgrade, "```bash\n")[1:] {
		script, _, _ := strings.Cut(block, "```")
		if strings.Contains(script, "--arg host") && strings.Contains(script, "homelab plan") {
			targetCheck = script
			break
		}
	}
	if targetCheck == "" {
		t.Fatal("the upgrade runbook has no executable target check")
	}

	fixture := newSiteFixture(t)
	fixture.mediaHost = strings.Replace(fixture.mediaHost, "name: media", "name: laptop", 1)
	fixture.mediaInstance = strings.Replace(fixture.mediaInstance, "host: media", "host: laptop", 1)
	homelab := secondMediaInstance(fixture.mediaInstance, false, false, false)
	homelab = strings.Replace(homelab, "host: laptop", "host: homelab", 1)
	fixture.addInstance("media-two.yaml", homelab)
	fixture.utilityHost = strings.Replace(fixture.mediaHost, "name: laptop", "name: homelab", 1)
	root := fixture.write(t)
	fixtureEnv := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_COUNT=0")
	run := func(name string, args ...string) string {
		t.Helper()
		command := exec.Command(name, args...)
		command.Dir = root
		command.Env = fixtureEnv
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("git", "init", "-q", "-b", "main")
	run("git", "add", "sites", "services")
	commit := func(message string) {
		t.Helper()
		run("git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test",
			"commit", "-qm", message, "--allow-empty")
	}
	commit("Candidate")
	candidate := run("git", "rev-parse", "HEAD")
	commit("Advance main")
	tip := run("git", "rev-parse", "HEAD")
	run("git", "switch", "--detach", candidate)

	if err := os.Mkdir(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, homelabCommand(t).Path, filepath.Join(root, "bin", "homelab"), 0o700)
	machine := run("hostname")
	for _, test := range []struct {
		name, instance, host, environment, expectedMachine, revision string
		wantTarget                                                   bool
	}{
		{"local laptop Staging", "media", "laptop", "staging", machine, candidate, true},
		{"homelab Production preview target", "media-two", "homelab", "production", machine, candidate, true},
		{"wrong execution machine", "media-two", "homelab", "staging", "unexpected-host.invalid", candidate, false},
		{"wrong inventory Host", "media", "homelab", "staging", machine, candidate, false},
		{"missing Environment", "media", "laptop", "", machine, candidate, false},
		{"moving main used instead of recorded revision", "media", "laptop", "staging", machine, tip, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", "-c", targetCheck)
			command.Dir = root
			command.Env = append(fixtureEnv, "HOMELAB_REPOSITORY_ROOT="+root,
				"site=fixture", "instance="+test.instance, "assigned_host="+test.host,
				"environment="+test.environment, "expected_hostname="+test.expectedMachine, "expected_sha="+test.revision)
			output, err := command.Output()
			if !test.wantTarget {
				if err == nil || len(output) != 0 {
					t.Fatalf("invalid target yielded %s (error %v)", output, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("target check failed: %v\n%s", err, output)
			}
			var target struct {
				sitePlanOutput
				Hostname string `json:"hostname"`
				Commit   string `json:"commit"`
			}
			if err := json.Unmarshal(output, &target); err != nil {
				t.Fatalf("decode target: %v\n%s", err, output)
			}
			if target.Hostname != machine || target.Host != test.host || target.Site != "fixture" ||
				target.Instance != test.instance || target.Environment != test.environment || target.Commit != candidate {
				t.Fatalf("unexpected target: %s", output)
			}
		})
	}
}

func TestHomeDeclaresMediaUpgradeTargetsOnBothHosts(t *testing.T) {
	command := homelabCommand(t)
	for _, host := range []string{"homelab", "laptop"} {
		for _, environment := range []string{"staging", "production"} {
			t.Run(host+"/"+environment, func(t *testing.T) {
				instance := "media-" + host
				plan := exec.Command(command.Path, "plan", "--site", "home", "--instance", instance,
					"--environment", environment, "--output", "json")
				plan.Dir = command.Dir
				output, err := plan.CombinedOutput()
				if err != nil {
					t.Fatalf("resolve target: %v\n%s", err, output)
				}
				var target sitePlanOutput
				if err := json.Unmarshal(output, &target); err != nil {
					t.Fatalf("decode target: %v\n%s", err, output)
				}
				if target.Site != "home" || target.Host != host || target.Instance != instance ||
					target.Environment != environment || !strings.HasPrefix(target.Plan, "name: media-"+environment+"\n") {
					t.Fatalf("unexpected Media Stack target for %s %s", instance, environment)
				}
			})
		}
	}
}
