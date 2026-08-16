package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/adkulas/homelab/internal/config"
	"github.com/adkulas/homelab/internal/topology"
)

const usage = "usage: media-stack plan --environment production|staging [--config path] [--versions path]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(64)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "plan" {
		return fmt.Errorf("%s", usage)
	}

	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "stacks/media/media-stack.yaml", "Declared Configuration path")
	versionsPath := flags.String("versions", "stacks/media/versions.yaml", "checked-in versions path")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if *environmentName == "" {
		return fmt.Errorf("environment is required\n%s", usage)
	}

	declared, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := declared.SelectEnvironment(*environmentName); err != nil {
		return err
	}
	versions, err := config.LoadVersions(*versionsPath)
	if err != nil {
		return err
	}

	compose, err := topology.Render(declared.Spec.Defaults, versions.Images)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(compose)
	return err
}
