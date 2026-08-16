package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/adkulas/homelab/internal/engine"
)

const usage = "usage: media-stack plan --environment production|staging [--config path]"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(64)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "plan" {
		return fmt.Errorf("%s", usage)
	}

	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if *environmentName == "" {
		return fmt.Errorf("environment is required\n%s", usage)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate working directory: %w", err)
	}
	request, err := engine.NewPlanRequest(workingDirectory, *environmentName, *configPath)
	if err != nil {
		return err
	}
	plan, err := engine.New().Plan(ctx, request)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(plan.Compose())
	return err
}
