// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"sync"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// Dispatcher routes inbound IPC messages and coordinates child lifecycles.
type Dispatcher struct {
	conn   *ipc.Conn
	runner *Runner

	mu         sync.Mutex
	cancelFn   context.CancelFunc
	runningPID int
	wg         sync.WaitGroup
}

// NewDispatcher constructs a Dispatcher bound to conn.
func NewDispatcher(conn *ipc.Conn) *Dispatcher {
	return &Dispatcher{conn: conn, runner: NewRunner()}
}

// Handle processes an inbound message from the host.
func (d *Dispatcher) Handle(msg ipc.OutboxMessage) {
	switch msg.Header.MsgType {
	case ipc.MsgHello:
		_ = d.SendControl(ipc.MsgHelloAck, ipc.HelloAckPayload{ProtocolVersion: ipc.ProtocolVersion}, false)
	case ipc.MsgStartChild:
		var p ipc.StartChildPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "decode", Message: err.Error()}, false)
			return
		}
		d.startChild(p)
	case ipc.MsgStopChild:
		var p ipc.StopChildPayload
		_ = ipc.DecodePayload(msg.Payload, &p)
		d.stopChild(time.Duration(p.TimeoutMs) * time.Millisecond)
	case ipc.MsgPing:
		_ = d.SendControl(ipc.MsgPong, nil, false)
	case ipc.MsgShutdown:
		d.stopChild(5 * time.Second)
	}
}

func (d *Dispatcher) startChild(p ipc.StartChildPayload) {
	d.mu.Lock()
	if d.cancelFn != nil {
		d.mu.Unlock()
		_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "start", Message: "child already running"}, false)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancelFn = cancel
	d.mu.Unlock()

	d.installLogSinks(d)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		res, err := d.runner.Run(ctx, RunSpec{
			Command:     p.Command,
			Args:        p.Args,
			Env:         p.Env,
			WorkDir:     p.WorkDir,
			StopTimeout: time.Duration(p.StopTimeout) * time.Millisecond,
			OnStarted: func(pid int) {
				d.mu.Lock()
				d.runningPID = pid
				d.mu.Unlock()
				_ = d.SendControl(ipc.MsgChildStarted, ipc.ChildStartedPayload{
					PID:       int32(pid),
					StartedAt: time.Now().UnixNano(),
				}, false)
			},
		})
		if err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "exec", Message: err.Error()}, false)
			d.clearRunning()
			return
		}

		_ = d.SendControl(ipc.MsgChildExited, ipc.ChildExitedPayload{
			PID:      int32(res.PID),
			ExitCode: int32(res.ExitCode),
			Signal:   int32(res.Signal),
			ExitedAt: res.ExitedAt.UnixNano(),
			Reason:   res.Reason,
		}, false)

		d.clearRunning()
	}()
}

// stopChild cancels the running child and waits for the runner goroutine to
// exit. The timeout parameter is part of the wire protocol but is enforced
// inside Runner via RunSpec.StopTimeout, not here: cancel() triggers the
// SIGTERM→SIGKILL escalation, after which Runner.Run returns and wg.Done()
// fires. wg.Wait() is therefore bounded by StopTimeout.
func (d *Dispatcher) stopChild(_ time.Duration) {
	d.mu.Lock()
	cancel := d.cancelFn
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	d.wg.Wait()
}

func (d *Dispatcher) clearRunning() {
	d.mu.Lock()
	d.cancelFn = nil
	d.runningPID = 0
	d.mu.Unlock()
}

// installLogSinks replaces the runner's default io.Discard sinks with
// chunkWriter instances that forward bytes to the host via MsgLogChunk.
// Extracted from startChild so it can be unit-tested without a real Conn.
func (d *Dispatcher) installLogSinks(sender chunkSender) {
	d.runner.StdoutSink = newChunkWriter(sender, 0) // 0 = stdout
	d.runner.StderrSink = newChunkWriter(sender, 1) // 1 = stderr
}

// SendControl serializes payload and enqueues it as an outbound frame.
func (d *Dispatcher) SendControl(t ipc.MsgType, payload any, ackRequired bool) error {
	var data []byte
	var err error
	if payload != nil {
		data, err = ipc.EncodePayload(payload)
		if err != nil {
			return err
		}
	}
	h := ipc.Header{
		MsgType: t,
		SeqNo:   d.conn.Seqs().Next(),
		Length:  uint32(len(data)),
	}
	if ackRequired {
		h.Flags |= ipc.FlagAckRequired
	}
	d.conn.Send(ipc.OutboxMessage{Header: h, Payload: data})
	return nil
}

func ctxWithTimeoutSeconds(n int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
}
