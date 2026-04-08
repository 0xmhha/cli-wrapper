// SPDX-License-Identifier: Apache-2.0

// Package config loads and validates cliwrap YAML configuration.
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseByteSize parses strings like "256MiB" or "1GB" into a byte count.
// Supported suffixes:
//
//	B, KB, MB, GB, TB        (powers of 1000)
//	KiB, MiB, GiB, TiB       (powers of 1024)
//
// An unsuffixed integer is interpreted as bytes. Whitespace between the
// number and suffix is allowed.
func ParseByteSize(s string) (uint64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("config: empty byte size")
	}

	// Split number and suffix.
	i := 0
	for i < len(trimmed) && (trimmed[i] >= '0' && trimmed[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("config: invalid byte size %q", s)
	}
	numPart := trimmed[:i]
	suffix := strings.TrimSpace(trimmed[i:])

	n, err := strconv.ParseUint(numPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: parse number in %q: %w", s, err)
	}

	mult, ok := byteMultiplier(suffix)
	if !ok {
		return 0, fmt.Errorf("config: unknown byte suffix %q", suffix)
	}
	return n * mult, nil
}

func byteMultiplier(suffix string) (uint64, bool) {
	switch suffix {
	case "", "B":
		return 1, true
	case "KB":
		return 1_000, true
	case "MB":
		return 1_000_000, true
	case "GB":
		return 1_000_000_000, true
	case "TB":
		return 1_000_000_000_000, true
	case "KiB":
		return 1024, true
	case "MiB":
		return 1024 * 1024, true
	case "GiB":
		return 1024 * 1024 * 1024, true
	case "TiB":
		return 1024 * 1024 * 1024 * 1024, true
	default:
		return 0, false
	}
}
