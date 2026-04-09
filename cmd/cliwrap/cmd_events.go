// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/mgmt"
)

func eventsCommand(args []string) int {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	processFilter := fs.String("process", "", "comma-separated list of process ids to filter; empty = all processes")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap events: %v\n", err)
		return 1
	}
	defer func() { _ = cli.Close() }()

	payload := mgmt.EventsSubscribePayload{
		ProcessIDs: parseProcessFilter(*processFilter),
	}
	if err := cli.Stream(mgmt.MsgEventsSubscribe, payload); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap events: subscribe: %v\n", err)
		return 1
	}

	for {
		h, body, err := cli.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			// A closed connection on the client side (e.g. user ctrl-c
			// triggering Close via deferred cli.Close) can return a
			// network-level error. Treat it as clean termination.
			return 0
		}
		if h.MsgType != mgmt.MsgEventsStream {
			fmt.Fprintf(os.Stderr, "cliwrap events: unexpected message type 0x%02x\n", h.MsgType)
			return 1
		}
		var ev mgmt.EventsStreamPayload
		if err := ipc.DecodePayload(body, &ev); err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap events: decode: %v\n", err)
			continue
		}
		ts := time.Unix(0, ev.Timestamp).Format("15:04:05.000")
		fmt.Printf("%s  %-32s  %-20s  %s\n", ts, ev.Type, ev.ProcessID, ev.Summary)
	}
}

// parseProcessFilter splits a comma-separated process list into a slice.
// Empty or whitespace-only input returns nil (meaning "subscribe to all").
func parseProcessFilter(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
