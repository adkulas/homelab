package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adkulas/homelab/internal/siteinventory"
)

const usage = `usage:
  homelab site validate --site name [--output human|json]
  homelab plan --site name --instance name --environment production|staging [--output human|json]`

type validationFailure struct{}

func (validationFailure) Error() string { return "Site validation failed" }

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var invalid validationFailure
		if errors.As(err, &invalid) {
			os.Exit(1)
		}
		os.Exit(64)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("%s", usage)
	}
	if arguments[0] == "site" && len(arguments) > 1 && arguments[1] == "validate" {
		return runSiteValidate(ctx, arguments[2:])
	}
	if arguments[0] == "plan" {
		return runPlan(ctx, arguments[1:])
	}
	return fmt.Errorf("%s", usage)
}

func runPlan(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	siteName := flags.String("site", "", "Site name")
	instanceName := flags.String("instance", "", "Stack Instance name")
	environment := flags.String("environment", "", "Production or Staging Environment")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *siteName == "" || *instanceName == "" || *environment == "" {
		return fmt.Errorf("site, instance, and environment are required\n%s", usage)
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	repositoryRoot, err := repositoryRoot()
	if err != nil {
		return err
	}
	report, err := siteinventory.Plan(ctx, repositoryRoot, siteinventory.SiteName(*siteName), siteinventory.StackInstanceName(*instanceName), siteinventory.EnvironmentName(*environment))
	if err != nil {
		if *output == "json" {
			diagnostic := siteinventory.Diagnostic{Code: "SITE_PLAN_FAILED", Explanation: "the selected Stack Instance could not be planned.", Remedy: "Correct the Site Inventory and retry."}
			var coded siteinventory.CodedError
			if errors.As(err, &coded) {
				diagnostic = siteinventory.Diagnostic{Code: coded.Code, Explanation: coded.Explanation, Remedy: coded.Remedy}
			}
			failure := struct {
				APIVersion  string                     `json:"apiVersion"`
				Kind        string                     `json:"kind"`
				Site        string                     `json:"site"`
				Instance    string                     `json:"instance"`
				Environment string                     `json:"environment"`
				Diagnostics []siteinventory.Diagnostic `json:"diagnostics"`
			}{"homelab.site/v1alpha1", "StackPlan", *siteName, *instanceName, *environment, []siteinventory.Diagnostic{diagnostic}}
			if encodeErr := json.NewEncoder(os.Stdout).Encode(failure); encodeErr != nil {
				return encodeErr
			}
			return validationFailure{}
		}
		return err
	}
	if *output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(report)
	}
	fmt.Fprintf(os.Stdout, "SITE_PLAN Site %s, Stack Instance %s, Host %s, %s Environment\n", report.Site, report.Instance, report.Host, report.Environment)
	for _, source := range report.Sources {
		fmt.Fprintf(os.Stdout, "source: %s\n", source)
	}
	for _, reference := range report.SensitiveReferences {
		fmt.Fprintf(os.Stdout, "sensitive reference: %s=%s\n", reference.ID, reference.Value)
	}
	fmt.Fprintln(os.Stdout, "---")
	_, err = fmt.Fprint(os.Stdout, report.Plan)
	return err
}

func runSiteValidate(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("site validate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	siteName := flags.String("site", "", "Site name")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *siteName == "" {
		return fmt.Errorf("site is required\n%s", usage)
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	repositoryRoot, err := repositoryRoot()
	if err != nil {
		return err
	}
	report := siteinventory.Validate(ctx, repositoryRoot, siteinventory.SiteName(*siteName))
	if *output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Fprint(os.Stdout, report.Human())
	}
	if !report.Valid {
		return validationFailure{}
	}
	return nil
}

func repositoryRoot() (string, error) {
	if root := os.Getenv("HOMELAB_REPOSITORY_ROOT"); root != "" {
		return filepath.Abs(root)
	}
	executable, err := os.Executable()
	if err == nil {
		for current := filepath.Dir(executable); ; current = filepath.Dir(current) {
			if _, statErr := os.Stat(filepath.Join(current, "go.mod")); statErr == nil {
				return current, nil
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate working directory: %w", err)
	}
	for current := workingDirectory; ; current = filepath.Dir(current) {
		if _, statErr := os.Stat(filepath.Join(current, "go.mod")); statErr == nil {
			return current, nil
		}
		if filepath.Dir(current) == current {
			return "", fmt.Errorf("locate homelab repository root from %q", workingDirectory)
		}
	}
}
