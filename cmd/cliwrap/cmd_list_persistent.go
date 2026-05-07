// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// listPersistentCommand implements `cliwrap list --persistent`. It reads
// session metadata from <persistentDir> directly (no running manager
// required) and prints a table to stdout.
//
// CW-G4 Task 17.
func listPersistentCommand(persistentDirOverride string) int {
	persistentDir := persistentDirOverride
	if persistentDir == "" {
		persistentDir = os.Getenv("CLIWRAP_PERSISTENT_DIR")
	}

	// Construct a Manager just for ListPersistent. WithAgentPath and
	// WithRuntimeDir are required by NewManager but not used by
	// ListPersistent; pass safe defaults.
	cliwrapAgentPath := os.Getenv("CLIWRAP_AGENT_PATH")
	if cliwrapAgentPath == "" {
		cliwrapAgentPath = "/usr/bin/true"
	}
	opts := []cliwrap.ManagerOption{
		cliwrap.WithAgentPath(cliwrapAgentPath),
		cliwrap.WithRuntimeDir(filepath.Join(os.TempDir(), "cliwrap-list-persistent")),
	}
	if persistentDir != "" {
		opts = append(opts, cliwrap.WithPersistentDir(persistentDir))
	}
	mgr, err := cliwrap.NewManager(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list --persistent: %v\n", err)
		return 1
	}
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	infos, err := mgr.ListPersistent()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list --persistent: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPID\tSTARTED\tALIVE\tATTACHED")
	for _, i := range infos {
		alive := "false  stale (rm -rf to clean)"
		if i.Alive {
			alive = "true"
		}
		attached := "no"
		if i.Attached {
			attached = "yes"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
			i.ID, i.AgentPID,
			i.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
			alive, attached)
	}
	_ = tw.Flush()
	return 0
}
