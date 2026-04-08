// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/mgmt"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
	"github.com/0xmhha/cli-wrapper/pkg/config"
)

func runCommand(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	path := fs.String("f", "cliwrap.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap run: %v\n", err)
		return 1
	}
	if cfg.Runtime.Dir == "" {
		fmt.Fprintln(os.Stderr, "cliwrap run: runtime.dir is required")
		return 1
	}
	agentPath := cfg.Runtime.AgentPath
	if agentPath == "" {
		// Fall back to binary alongside this one.
		self, _ := os.Executable()
		agentPath = filepath.Join(filepath.Dir(self), "cliwrap-agent")
	}

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentPath),
		cliwrap.WithRuntimeDir(cfg.Runtime.Dir),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap run: %v\n", err)
		return 1
	}

	// Write the runtime dir to a well-known cache file so that other
	// subcommands (e.g. cliwrap list) can discover the manager socket
	// without requiring the --runtime-dir flag.
	writeRuntimeHint(cfg.Runtime.Dir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	handles := make([]cliwrap.ProcessHandle, 0)
	for _, g := range cfg.Groups {
		for _, spec := range g.Processes {
			h, err := mgr.Register(spec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cliwrap run: register %s: %v\n", spec.ID, err)
				_ = mgr.Shutdown(ctx)
				return 1
			}
			handles = append(handles, h)
		}
	}

	// Start management socket.
	srv, err := mgmt.NewServer(mgmt.ServerOptions{
		SocketPath: filepath.Join(cfg.Runtime.Dir, "manager.sock"),
		Manager:    mgr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap run: mgmt server: %v\n", err)
		_ = mgr.Shutdown(ctx)
		return 1
	}
	_ = srv.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	for _, h := range handles {
		if err := h.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap run: start %s: %v\n", h.ID(), err)
		}
	}

	fmt.Printf("cliwrap: running %d processes; Ctrl-C to exit\n", len(handles))
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = mgr.Shutdown(shutdownCtx)
	return 0
}

// writeRuntimeHint persists the runtime directory to a well-known cache path
// so that client commands (list, status, stop) running in a different terminal
// can locate the manager socket without explicit --runtime-dir.
func writeRuntimeHint(dir string) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	hintDir := filepath.Join(cacheDir, "cliwrap")
	_ = os.MkdirAll(hintDir, 0o700)
	_ = os.WriteFile(filepath.Join(hintDir, "runtime-dir"), []byte(dir), 0o600)
}
