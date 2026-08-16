package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/adkulas/homelab/internal/engine"
)

const usage = `usage:
  media-stack init --environment production|staging [--config path] --non-interactive --answers path
  media-stack doctor --environment production|staging [--config path] [--output human|json]
  media-stack plan --environment production|staging [--config path]`

type operationalFailure struct{}

func (operationalFailure) Error() string { return "one or more prerequisite checks failed" }

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var operational operationalFailure
		if errors.As(err, &operational) {
			os.Exit(1)
		}
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
	case "doctor":
		return runDoctor(ctx, arguments[1:])
	case "plan":
		return runPlan(ctx, arguments[1:])
	default:
		return fmt.Errorf("%s", usage)
	}
}

func runDoctor(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *environmentName == "" {
		return fmt.Errorf("environment is required\n%s", usage)
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate working directory: %w", err)
	}
	request, err := engine.NewDoctorRequest(workingDirectory, *environmentName, *configPath)
	if err != nil {
		return err
	}
	report, err := engine.New().Doctor(ctx, request)
	if err != nil {
		return err
	}
	if *output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else {
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintf(os.Stdout, "%s %-5s %s", diagnostic.Code, diagnostic.Status, diagnostic.Explanation)
			if diagnostic.Status == "fail" {
				fmt.Fprintf(os.Stdout, "; remedy: %s", diagnostic.Remedy)
			}
			fmt.Fprintln(os.Stdout)
		}
	}
	if report.Failed() {
		return operationalFailure{}
	}
	return nil
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
