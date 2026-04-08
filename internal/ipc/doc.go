// SPDX-License-Identifier: Apache-2.0

// Package ipc implements the cli-wrapper inter-process communication layer.
//
// It provides a length-prefixed framed protocol over a Unix Domain Socket.
// Each frame carries a 17-byte header, a MessagePack-encoded payload, and
// a monotonic sequence number. The package also implements a persistent
// outbox with a write-ahead log (WAL) so that message delivery survives
// transient connection loss and producer bursts.
//
// This package is consumed by both the host-side controller and the
// cliwrap-agent subprocess.
package ipc
