// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/mgmt"
)

func logsCommand(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	stream := fs.String("stream", "all", "which stream to show: stdout | stderr | all")
	follow := fs.Bool("follow", false, "stream new log chunks as they arrive (tail -f style)")
	fs.BoolVar(follow, "f", false, "alias for --follow")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap logs [--stream stdout|stderr|all] [--follow|-f] <id>")
		return 2
	}
	id := fs.Arg(0)

	// Resolve which streams to request.
	streams, err := parseStreamFlag(*stream)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
		return 2
	}

	// Follow mode requires a single stream: one persistent connection
	// drains one server-side WatchLogs subscription. The default
	// --stream=all is ambiguous for follow; reject explicitly with
	// an actionable message.
	if *follow && len(streams) != 1 {
		fmt.Fprintln(os.Stderr, "cliwrap logs: --follow requires --stream=stdout or --stream=stderr (not 'all')")
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
		return 1
	}
	defer func() { _ = cli.Close() }()

	if *follow {
		return followOneStream(cli, id, streams[0])
	}

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

// printOneStream issues a single MsgLogsRequest (snapshot mode, Follow=false)
// and writes the returned bytes to stdout (for stream 0) or stderr (for
// stream 1). The stderr routing matches user expectation: `cliwrap logs
// p1 2>/dev/null` should hide the stderr side of the supervised child.
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
	return writeStreamBytes(stream, resp.Data)
}

// followOneStream issues a MsgLogsRequest with Follow=true, prints the
// initial snapshot frame, then loops reading subsequent stream frames
// until the server closes the connection or an error occurs.
func followOneStream(cli *mgmt.Client, id string, stream uint8) int {
	if err := cli.Stream(mgmt.MsgLogsRequest, mgmt.LogsRequestPayload{
		ID:     id,
		Stream: stream,
		Follow: true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: subscribe: %v\n", err)
		return 1
	}

	for {
		h, body, err := cli.ReadFrame()
		if err != nil {
			// Clean termination signals: the server closed its side
			// (EOF / ErrUnexpectedEOF) or our deferred cli.Close
			// closed the socket from underneath us (ErrClosed).
			// Everything else is a real transport or protocol
			// failure that the user and any shell pipeline need to
			// see — do NOT silently swallow it.
			if errors.Is(err, io.EOF) ||
				errors.Is(err, io.ErrUnexpectedEOF) ||
				errors.Is(err, net.ErrClosed) {
				return 0
			}
			fmt.Fprintf(os.Stderr, "cliwrap logs: read: %v\n", err)
			return 1
		}
		if h.MsgType != mgmt.MsgLogsStream {
			fmt.Fprintf(os.Stderr, "cliwrap logs: unexpected message type 0x%02x\n", h.MsgType)
			return 1
		}
		var resp mgmt.LogsStreamPayload
		if err := ipc.DecodePayload(body, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap logs: decode: %v\n", err)
			continue
		}
		if len(resp.Data) > 0 {
			if werr := writeStreamBytes(stream, resp.Data); werr != nil {
				fmt.Fprintf(os.Stderr, "cliwrap logs: write: %v\n", werr)
				return 1
			}
		}
		// EOF=true would mean "stream finished" — in follow mode the
		// server keeps EOF=false for every frame, but if we ever see
		// true we should treat it as a clean end-of-stream.
		if resp.EOF {
			return 0
		}
	}
}

// writeStreamBytes writes payload bytes to stdout (stream 0) or
// stderr (stream 1). Centralized so both snapshot and follow modes
// share the same routing semantics.
func writeStreamBytes(stream uint8, data []byte) error {
	sink := os.Stdout
	if stream == 1 {
		sink = os.Stderr
	}
	_, err := sink.Write(data)
	return err
}
