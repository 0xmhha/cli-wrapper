// SPDX-License-Identifier: Apache-2.0

// fixture-crasher exits with a specific code after a delay.
package main

import (
	"flag"
	"os"
	"time"
)

func main() {
	after := flag.Duration("after", 500*time.Millisecond, "exit after this delay")
	code := flag.Int("code", 99, "exit code")
	flag.Parse()
	time.Sleep(*after)
	os.Exit(*code)
}
