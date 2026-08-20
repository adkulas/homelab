package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/adkulas/homelab/internal/engine"
	"github.com/adkulas/homelab/internal/storageprobe"
)

const usage = `usage:
  media-stack init --environment production|staging [--config path] --non-interactive --answers path
  media-stack doctor --environment production|staging [--config path] [--output human|json]
  media-stack plan --environment production|staging [--config path]
  media-stack apply --environment production|staging [--config path]
  media-stack backup --environment production|staging [--config path] [--label text] [--protect] [--output human|json]
  media-stack restore --environment production|staging --backup path [--config path] [--confirm] [--as-restore-drill] [--output human|json]
  media-stack verify --environment production|staging [--config path] --suite full|promotion [--legal-fixture path] [--legal-series-fixture path] [--output human|json]
  media-stack test [--run pattern]`

type operationalFailure struct {
	cause error
}

func (failure operationalFailure) Error() string {
	if failure.cause != nil {
		return failure.cause.Error()
	}
	return "one or more prerequisite checks failed"
}

func (failure operationalFailure) Unwrap() error { return failure.cause }

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
	case "apply":
		return runApply(ctx, arguments[1:])
	case "backup":
		return runBackup(ctx, arguments[1:])
	case "restore":
		return runRestore(ctx, arguments[1:])
	case "verify":
		return runVerify(ctx, arguments[1:])
	case "test":
		return runTest(arguments[1:])
	case "__storage-probe":
		return runStorageProbe(arguments[1:])
	default:
		return fmt.Errorf("%s", usage)
	}
}

func runVerify(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	suite := flags.String("suite", "full", "verification suite")
	output := flags.String("output", "human", "human or json")
	legalFixture := flags.String("legal-fixture", "", "legal movie fixture for Promotion verification")
	legalSeriesFixture := flags.String("legal-series-fixture", "", "legal series fixture for Promotion verification")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *environmentName == "" {
		return fmt.Errorf("environment is required\n%s", usage)
	}
	if *environmentName == "production" {
		if *suite == "promotion" {
			return fmt.Errorf("Promotion verification is disruptive and requires the Staging Environment")
		}
		return fmt.Errorf("full verification is disruptive and requires the Staging Environment")
	}
	if *suite != "full" && *suite != "promotion" {
		return fmt.Errorf("suite must be full or promotion")
	}
	if *suite == "promotion" && *legalFixture == "" && *legalSeriesFixture == "" {
		return fmt.Errorf("Promotion verification requires --legal-fixture or --legal-series-fixture")
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate working directory: %w", err)
	}
	request, err := engine.NewVerifyRequest(workingDirectory, *environmentName, *configPath, *suite, *legalFixture, *legalSeriesFixture)
	if err != nil {
		return err
	}
	report, err := engine.New().Verify(ctx, request)
	if err != nil {
		return operationalFailure{cause: err}
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

func runBackup(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	label := flags.String("label", "", "optional backup label")
	protect := flags.Bool("protect", false, "protect backup from retention")
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
	request, err := engine.NewBackupRequest(workingDirectory, *environmentName, *configPath, *label, *protect)
	if err != nil {
		return err
	}
	report, err := engine.New().Backup(ctx, request)
	if err != nil {
		return err
	}
	if *output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(report)
	}
	fmt.Fprintf(os.Stdout, "Prepared %s backup manifest covering %d mutable service volumes in the %s Environment.\n", report.ProjectName, len(report.Services), report.Environment)
	return nil
}

func runRestore(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	environmentName := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "Declared Configuration path")
	backupPath := flags.String("backup", "", "backup manifest path")
	confirm := flags.Bool("confirm", false, "confirm the restore preview")
	asRestoreDrill := flags.Bool("as-restore-drill", false, "permit production-to-staging drill restores")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *environmentName == "" {
		return fmt.Errorf("environment is required\n%s", usage)
	}
	if *backupPath == "" {
		return fmt.Errorf("backup is required\n%s", usage)
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate working directory: %w", err)
	}
	request, err := engine.NewRestoreRequest(workingDirectory, *environmentName, *configPath, *backupPath, *confirm, *asRestoreDrill)
	if err != nil {
		return err
	}
	report, err := engine.New().Restore(ctx, request)
	if err != nil {
		return operationalFailure{cause: err}
	}
	if *output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(report)
	}
	fmt.Fprintln(os.Stdout, report.Preview)
	return nil
}

func runTest(arguments []string) error {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	runPattern := flags.String("run", "", "optional go test -run pattern")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	testArgs := []string{"test", "./..."}
	if *runPattern != "" {
		testArgs = append(testArgs, "-run", *runPattern)
	}
	command := exec.Command("go", testArgs...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	return command.Run()
}

func runApply(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
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
	request, err := engine.NewApplyRequest(workingDirectory, *environmentName, *configPath)
	if err != nil {
		return err
	}
	report, err := engine.New().Apply(ctx, request)
	if err != nil {
		return operationalFailure{cause: err}
	}
	fmt.Fprintf(os.Stdout, "Applied the pinned Movie Library policy through Profilarr in the %s Environment.\n", report.Environment)
	return nil
}

func runStorageProbe(arguments []string) error {
	flags := flag.NewFlagSet("__storage-probe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	source := flags.String("source", "", "source directory")
	destination := flags.String("destination", "", "destination directory")
	uid := flags.Int("uid", -1, "expected numeric UID")
	gid := flags.Int("gid", -1, "expected numeric GID")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *source == "" || *destination == "" || *uid < 0 || *gid < 0 {
		return fmt.Errorf("storage probe source, destination, uid, and gid are required")
	}
	return json.NewEncoder(os.Stdout).Encode(storageprobe.Run(*source, *destination, *uid, *gid))
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
