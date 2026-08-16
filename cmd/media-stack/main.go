package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/adkulas/homelab/internal/engine"
)

const usage = `usage:
  media-stack init --environment production|staging [--config path] --non-interactive --answers path
  media-stack plan --environment production|staging [--config path]`

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(64)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "init":
		return runInit(ctx, arguments[1:])
	case "plan":
		return runPlan(ctx, arguments[1:])
	default:
		return fmt.Errorf("%s", usage)
	}
}

func runPlan(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	if err := flags.Parse(arguments); err != nil {
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

func runInit(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	nonInteractive := flags.Bool("non-interactive", false, "Read all answers from a file")
	answersPath := flags.String("answers", "", "Initialization answers path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *environmentName == "" {
		return fmt.Errorf("environment is required\n%s", usage)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate working directory: %w", err)
	}
	var request engine.InitRequest
	if *nonInteractive {
		request, err = engine.NewInitRequest(workingDirectory, *environmentName, *configPath, *answersPath)
	} else {
		request, err = engine.NewInteractiveInitRequest(workingDirectory, *environmentName, *configPath, os.Stdin, os.Stdout)
	}
	if err != nil {
		return err
	}
	report, err := engine.New().Init(ctx, request)
	if err != nil {
		return err
	}
	if report.Preserved {
		fmt.Fprintf(os.Stdout, "Preserved existing %s Environment choices and encrypted secrets.\n", report.Environment)
	} else {
		fmt.Fprintf(os.Stdout, "Initialized %s Environment Declared Configuration and encrypted secrets.\n", report.Environment)
	}
	return nil
}
