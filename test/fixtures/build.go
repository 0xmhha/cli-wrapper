// SPDX-License-Identifier: Apache-2.0

// Package fixtures provides a go:generate-friendly entry point for
// compiling the test binaries. To build them all:
//
//	go build -o test/fixtures/bin/fixture-echo   ./test/fixtures/echo
//	go build -o test/fixtures/bin/fixture-noisy  ./test/fixtures/noisy
//	go build -o test/fixtures/bin/fixture-crasher ./test/fixtures/crasher
//	go build -o test/fixtures/bin/fixture-hanger ./test/fixtures/hanger
package fixtures
