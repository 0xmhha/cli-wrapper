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
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	if err := agent.Run(ctx, agent.DefaultConfig()); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap-agent: %v\n", err)
		os.Exit(1)
	}
}
