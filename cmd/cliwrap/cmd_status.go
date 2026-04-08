package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
)

func statusCommand(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap status <id>")
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap status: %v\n", err)
		return 1
	}
	defer cli.Close()

	_, body, err := cli.Call(mgmt.MsgStatusRequest, mgmt.StatusRequestPayload{ID: fs.Arg(0)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap status: %v\n", err)
		return 1
	}
	var resp mgmt.StatusResponsePayload
	if err := ipc.DecodePayload(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap status: decode: %v\n", err)
		return 1
	}
	if resp.Err != "" {
		fmt.Fprintln(os.Stderr, resp.Err)
		return 1
	}
	fmt.Printf("ID:      %s\n", resp.Entry.ID)
	fmt.Printf("Name:    %s\n", resp.Entry.Name)
	fmt.Printf("State:   %s\n", resp.Entry.State)
	fmt.Printf("ChildPID:%d\n", resp.Entry.ChildPID)
	return 0
}
