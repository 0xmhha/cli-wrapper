// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeqGenerator_Monotonic(t *testing.T) {
	g := NewSeqGenerator(1)
	require.Equal(t, uint64(1), g.Next())
	require.Equal(t, uint64(2), g.Next())
	require.Equal(t, uint64(3), g.Next())
}

func TestSeqGenerator_Concurrent(t *testing.T) {
	g := NewSeqGenerator(1)
	const N = 1000
	const G = 8

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[uint64]struct{}, N*G)

	for i := 0; i < G; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < N; j++ {
				v := g.Next()
				mu.Lock()
				seen[v] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Len(t, seen, N*G, "all sequence numbers must be unique")
}

func TestDedupTracker(t *testing.T) {
	d := NewDedupTracker()

	require.False(t, d.Seen(1), "first seq is new")
	require.True(t, d.Seen(1), "repeat seq must be duplicate")
	require.False(t, d.Seen(2), "advancing seq is new")
	require.False(t, d.Seen(3), "advancing seq is new")
	require.True(t, d.Seen(2), "seq below watermark must be duplicate")
	require.True(t, d.Seen(3), "seq at watermark must be duplicate")
	require.False(t, d.Seen(5), "skip ahead is new")
	require.True(t, d.Seen(4), "seq below new watermark must be duplicate")
}

func TestDedupTracker_Concurrent(t *testing.T) {
	d := NewDedupTracker()
	const N = 1000
	const G = 8

	var wg sync.WaitGroup
	for i := 0; i < G; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 1; j <= N; j++ {
				d.Seen(uint64(base*N + j))
			}
		}(i)
	}
	wg.Wait()

	// After all goroutines, the watermark should have advanced.
	// All previously seen seqs must be duplicates.
	require.True(t, d.Seen(1), "seq 1 must be duplicate after concurrent writes")
}
