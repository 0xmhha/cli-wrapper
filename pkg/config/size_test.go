// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		err  bool
	}{
		{"0", 0, false},
		{"1024", 1024, false},
		{"1KiB", 1024, false},
		{"1KB", 1000, false},
		{"1MiB", 1024 * 1024, false},
		{"256MiB", 256 * 1024 * 1024, false},
		{"1 GiB", 1024 * 1024 * 1024, false},
		{"1GB", 1_000_000_000, false},
		{"garbage", 0, true},
		{"1XB", 0, true},
	}
	for _, c := range cases {
		got, err := ParseByteSize(c.in)
		if c.err {
			require.Error(t, err, c.in)
			continue
		}
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got, c.in)
	}
}
