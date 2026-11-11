package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/adkulas/homelab/internal/engine"
	"os"
)

func runDestroy(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("destroy", flag.ContinueOnError)
	environment := flags.String("environment", "", "Production or Staging Environment")
	configPath := flags.String("config", "", "explicit standalone Declared Configuration")
	selection := deploymentFlags(flags)
	planPath := flags.String("plan", "", "save observational destruction plan")
	executePath := flags.String("execute", "", "execute exact saved destruction plan")
	volumes := flags.Bool("volumes", false, "remove application volumes after protected final-state backup")
	secrets := flags.Bool("secrets", false, "remove encrypted application document; requires --volumes")
	downloads := flags.Bool("downloads", false, "remove selected torrents subtree")
	media := flags.Bool("media", false, "remove selected media subtree")
	confirm := flags.String("confirm", "", "SHA256 of exact saved plan")
	target := flags.String("target", "", "full Production target identity")
	loss := flags.String("acknowledge-loss", "", "exact target and payload-loss acknowledgement shown in preview")
	resume := flags.String("resume", "", "journal to reauthorize remaining work in a new preview")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *environment == "" {
		return fmt.Errorf("environment is required")
	}
	if (*planPath == "") == (*executePath == "") {
		return fmt.Errorf("exactly one of --plan or --execute is required")
	}
	if *secrets && !*volumes {
		return fmt.Errorf("--secrets requires --volumes")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	deployment, err := selection.resolve(ctx, cwd, *environment, *configPath)
	if err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected destroy arguments")
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	request, err := engine.NewDestroyRequest(cwd, *environment, *configPath, engine.DestroyOptions{Volumes: *volumes, Downloads: *downloads, Media: *media, Secrets: *secrets})
	if err != nil {
		return err
	}
	request = request.WithDeployment(deployment)
	request.ArtifactPath = *planPath
	request.Execute = *executePath != ""
	if request.Execute {
		request.ArtifactPath = *executePath
	}
	request.Confirm = *confirm
	request.Target = *target
	request.AcknowledgeLoss = *loss
	request.Resume = *resume
	report, err := engine.New().Destroy(ctx, request)
	if err != nil {
		if report.JournalPath != "" {
			if *output == "json" {
				json.NewEncoder(os.Stdout).Encode(report)
			} else {
				fmt.Fprint(os.Stderr, report.Human())
			}
		}
		return operationalFailure{cause: err}
	}
	if *output == "json" {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	fmt.Fprint(os.Stdout, report.Human())
	return nil
}
