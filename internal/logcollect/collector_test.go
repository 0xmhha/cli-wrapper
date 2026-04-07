package logcollect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollector_RoutesByProcessAndStream(t *testing.T) {
	c := NewCollector(CollectorOptions{RingBufferBytes: 1024})
	defer c.Close()

	c.Write(LogEntry{ProcessID: "p1", Stream: 0, Timestamp: time.Now(), Data: []byte("hello ")})
	c.Write(LogEntry{ProcessID: "p1", Stream: 0, Timestamp: time.Now(), Data: []byte("world")})
	c.Write(LogEntry{ProcessID: "p1", Stream: 1, Timestamp: time.Now(), Data: []byte("err")})
	c.Write(LogEntry{ProcessID: "p2", Stream: 0, Timestamp: time.Now(), Data: []byte("other")})

	require.Equal(t, []byte("hello world"), c.Snapshot("p1", 0))
	require.Equal(t, []byte("err"), c.Snapshot("p1", 1))
	require.Equal(t, []byte("other"), c.Snapshot("p2", 0))
}

func TestCollector_UnknownProcessReturnsNil(t *testing.T) {
	c := NewCollector(CollectorOptions{RingBufferBytes: 64})
	defer c.Close()
	require.Nil(t, c.Snapshot("missing", 0))
}
