package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/adkulas/homelab/internal/engine"
	"github.com/adkulas/homelab/internal/siteinventory"
)

type deploymentSelection struct{ site, instance *string }

func deploymentFlags(flags *flag.FlagSet) deploymentSelection {
	return deploymentSelection{flags.String("site", "", "Site name"), flags.String("instance", "", "Stack Instance name")}
}

func (selection deploymentSelection) resolve(ctx context.Context, workingDirectory, environment, explicitConfig string) (*engine.Deployment, error) {
	if *selection.site == "" && *selection.instance == "" && explicitConfig != "" {
		return nil, nil
	}
	if *selection.site == "" || *selection.instance == "" {
		return nil, fmt.Errorf("select --site and --instance, or supply an explicit standalone --config path")
	}
	if explicitConfig != "" {
		return nil, fmt.Errorf("--config cannot be combined with --site or --instance")
	}
	root, err := engine.RepositoryRoot(workingDirectory)
	if err != nil {
		return nil, err
	}
	return siteinventory.ResolveMedia(ctx, root, siteinventory.SiteName(*selection.site), siteinventory.StackInstanceName(*selection.instance), siteinventory.EnvironmentName(environment))
}
