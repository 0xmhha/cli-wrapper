// SPDX-License-Identifier: Apache-2.0

// fixture-noisy writes a heartbeat line to stdout and stderr every 100ms.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		<-t.C
		fmt.Fprintf(os.Stdout, "out %d\n", i)
		fmt.Fprintf(os.Stderr, "err %d\n", i)
	}
}
