package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
)

func stopCommand(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap stop <id> [<id>...]")
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap stop: %v\n", err)
		return 1
	}
	defer cli.Close()

	rc := 0
	for _, id := range fs.Args() {
		_, body, err := cli.Call(mgmt.MsgStopRequest, mgmt.StopRequestPayload{ID: id})
		if err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap stop %s: %v\n", id, err)
			rc = 1
			continue
		}
		var resp mgmt.StatusResponsePayload
		_ = ipc.DecodePayload(body, &resp)
		if resp.Err != "" {
			fmt.Fprintf(os.Stderr, "cliwrap stop %s: %s\n", id, resp.Err)
			rc = 1
		} else {
			fmt.Printf("stopped %s\n", id)
		}
	}
	return rc
}
