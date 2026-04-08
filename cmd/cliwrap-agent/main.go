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
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	return agent.Run(ctx, agent.DefaultConfig())
}
