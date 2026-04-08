package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: run, validate, list, status, stop, logs, events, version")
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var exit int
	switch cmd {
	case "run":
		exit = runCommand(args)
	case "validate":
		exit = validateCommand(args)
	case "list":
		exit = listCommand(args)
	case "status":
		exit = statusCommand(args)
	case "stop":
		exit = stopCommand(args)
	case "logs":
		exit = logsCommand(args)
	case "events":
		exit = eventsCommand(args)
	case "version":
		fmt.Println("cliwrap dev")
		exit = 0
	case "-h", "--help", "help":
		fmt.Println("cliwrap: manage background CLI processes")
		exit = 0
	default:
		fmt.Fprintf(os.Stderr, "cliwrap: unknown command %q\n", cmd)
		exit = 2
	}
	os.Exit(exit)
}
