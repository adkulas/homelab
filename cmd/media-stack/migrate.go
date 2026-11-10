package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adkulas/homelab/internal/engine"
)

func runMigrateSecrets(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("migrate-secrets", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	selection := deploymentFlags(flags)
	legacy := flags.String("legacy-config", "", "Legacy MediaStack configuration whose credentials will be copied")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *legacy == "" {
		return fmt.Errorf("migrate-secrets requires --legacy-config")
	}
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	deployment, err := selection.resolve(ctx, directory, "staging", "")
	if err != nil {
		return err
	}
	path := *legacy
	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}
	count, err := engine.MigrateSecrets(ctx, deployment, path)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Migrated %d Environment credential documents; preserved legacy sources.\n", count)
	return nil
}
