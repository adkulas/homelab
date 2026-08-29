package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/adkulas/homelab/internal/configcontract"
)

func runConfig(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "describe" {
		return fmt.Errorf("%s", usage)
	}
	flags := flag.NewFlagSet("config describe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	serviceName := flags.String("service", "", "service to describe")
	output := flags.String("output", "human", "human or json")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("config describe accepts no positional arguments")
	}
	if *output != "human" && *output != "json" {
		return fmt.Errorf("output must be human or json")
	}
	document, err := configcontract.Describe(*serviceName)
	if err != nil {
		return err
	}
	if *output == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(document)
	}
	_, err = fmt.Fprint(os.Stdout, configcontract.Human(document))
	return err
}
