package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cli-wrapper/cli-wrapper/pkg/config"
)

func validateCommand(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	path := fs.String("f", "cliwrap.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap: validate: %v\n", err)
		return 1
	}
	n := 0
	for _, g := range cfg.Groups {
		n += len(g.Processes)
	}
	fmt.Printf("OK: %d groups, %d processes\n", len(cfg.Groups), n)
	return 0
}
