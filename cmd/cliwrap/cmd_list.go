package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/mgmt"
)

func listCommand(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list: %v\n", err)
		return 1
	}
	defer func() { _ = cli.Close() }()

	_, body, err := cli.Call(mgmt.MsgListRequest, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list: %v\n", err)
		return 1
	}

	var resp mgmt.ListResponsePayload
	if err := ipc.DecodePayload(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list: decode: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tPID")
	for _, e := range resp.Entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", e.ID, e.Name, e.State, e.ChildPID)
	}
	_ = tw.Flush()
	return 0
}

func managerSocketPath(override string) string {
	if override != "" {
		return filepath.Join(override, "manager.sock")
	}
	if v := os.Getenv("CLIWRAP_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "manager.sock")
	}
	// Read the runtime-dir hint file written by `cliwrap run`.
	if cacheDir, err := os.UserCacheDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(cacheDir, "cliwrap", "runtime-dir")); err == nil {
			return filepath.Join(strings.TrimSpace(string(b)), "manager.sock")
		}
	}
	return filepath.Join(os.TempDir(), "cliwrap", "manager.sock")
}
