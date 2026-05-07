// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// killCommand implements `cliwrap kill <id>`. Reattaches to the persistent
// session, stops the child, and closes the connection. On stale sessions,
// prints a hint to rm -rf the directory directly.
//
// CW-G4 Task 17.
func killCommand(args []string) int {
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	persistentDir := fs.String("persistent-dir", "", "override persistent directory (default $HOME/.cliwrap/sessions)")
	timeout := fs.Duration("timeout", 10*time.Second, "max time to wait for the session to stop")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap kill <session-id>")
		return 2
	}
	id := fs.Arg(0)

	pdir := *persistentDir
	if pdir == "" {
		pdir = os.Getenv("CLIWRAP_PERSISTENT_DIR")
	}

	cliwrapAgentPath := os.Getenv("CLIWRAP_AGENT_PATH")
	if cliwrapAgentPath == "" {
		cliwrapAgentPath = "/usr/bin/true"
	}
	opts := []cliwrap.ManagerOption{
		cliwrap.WithAgentPath(cliwrapAgentPath),
		cliwrap.WithRuntimeDir(filepath.Join(os.TempDir(), "cliwrap-kill")),
	}
	if pdir != "" {
		opts = append(opts, cliwrap.WithPersistentDir(pdir))
	}
	mgr, err := cliwrap.NewManager(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap kill: %v\n", err)
		return 1
	}
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Fprintf(os.Stdout, "Connecting to session %s...\n", id)
	h, err := mgr.Reattach(ctx, id)
	if err != nil {
		if errors.Is(err, cliwrap.ErrAgentDead) {
			fmt.Fprintf(os.Stderr, "Session %s is stale (agent dead). Run `rm -rf <dir>/%s/` to clean up.\n", id, id)
			return 1
		}
		fmt.Fprintf(os.Stderr, "cliwrap kill: %v\n", err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "Stopping child...")
	if err := h.Stop(ctx); err != nil {
		_ = h.Close(ctx)
		fmt.Fprintf(os.Stderr, "cliwrap kill: stop: %v\n", err)
		return 1
	}
	if err := h.Close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap kill: close: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "Cleanup complete.")
	return 0
}
