// fixture-hanger ignores SIGTERM until it receives SIGKILL.
package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	for {
		select {
		case <-sigs:
			// Ignore and keep running.
		case <-time.After(10 * time.Second):
			os.Exit(0)
		}
	}
}
