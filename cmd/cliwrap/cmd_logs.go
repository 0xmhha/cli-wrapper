// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/mgmt"
)

func logsCommand(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	stream := fs.String("stream", "all", "which stream to show: stdout | stderr | all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap logs [--stream stdout|stderr|all] <id>")
		return 2
	}
	id := fs.Arg(0)

	// Resolve which streams to request.
	streams, err := parseStreamFlag(*stream)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
		return 1
	}
	defer func() { _ = cli.Close() }()

	for _, s := range streams {
		if err := printOneStream(cli, id, s); err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
			return 1
		}
	}
	return 0
}

// parseStreamFlag converts the --stream flag value into one or two
// stream ids to query. "all" returns [0, 1] (stdout then stderr).
func parseStreamFlag(v string) ([]uint8, error) {
	switch v {
	case "stdout":
		return []uint8{0}, nil
	case "stderr":
		return []uint8{1}, nil
	case "all", "":
		return []uint8{0, 1}, nil
	default:
		return nil, fmt.Errorf("unknown --stream value %q (want stdout, stderr, or all)", v)
	}
}

// printOneStream issues a single MsgLogsRequest and writes the
// returned bytes to stdout (for stream 0) or stderr (for stream 1).
// The stderr routing matches user expectation: `cliwrap logs p1 2>/dev/null`
// should hide the stderr side of the supervised child.
func printOneStream(cli *mgmt.Client, id string, stream uint8) error {
	_, body, err := cli.Call(mgmt.MsgLogsRequest, mgmt.LogsRequestPayload{
		ID:     id,
		Stream: stream,
	})
	if err != nil {
		return fmt.Errorf("call: %w", err)
	}
	var resp mgmt.LogsStreamPayload
	if err := ipc.DecodePayload(body, &resp); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil
	}
	var sink *os.File
	if stream == 1 {
		sink = os.Stderr
	} else {
		sink = os.Stdout
	}
	if _, err := sink.Write(resp.Data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
