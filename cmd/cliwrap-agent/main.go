// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xmhha/cli-wrapper/internal/agent"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap-agent: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	persistent := false
	for _, a := range os.Args[1:] {
		if a == "--persistent" {
			persistent = true
			break
		}
	}

	// CW-G4: in persistent mode the agent must NOT exit on SIGHUP — terminal
	// closure should not kill a daemonized session. Setsid (set by spawner)
	// already prevents SIGHUP from being delivered, but we also drop SIGHUP
	// from the signal context as defense-in-depth.
	signals := []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	if !persistent {
		signals = append(signals, syscall.SIGHUP)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), signals...)
	defer cancel()

	cfg := agent.DefaultConfig()
	cfg.Persistent = persistent
	return agent.Run(ctx, cfg)
}
