// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// Dispatcher routes inbound IPC messages and coordinates child lifecycles.
type Dispatcher struct {
	connMu sync.RWMutex
	conn   *ipc.Conn // protected by connMu; CW-G4 swaps it on reattach
	runner *Runner

	// CW-G4: persistent-mode lifecycle. When true, the dispatcher signals
	// shutdownReq after the child terminates (Stop or natural exit) so
	// agent.Run can exit cleanly and the metadata is cleaned up.
	persistent           bool
	shutdownMu           sync.Mutex
	shutdownReq          chan struct{}
	persistentSessionDir string // set when persistent; used to update meta.json on StartChild

	mu         sync.Mutex
	cancelFn   context.CancelFunc
	runningPID int
	wg         sync.WaitGroup

	// activeRunners tracks goroutines that have been started for child
	// supervision but have not yet returned from cmd.Wait(). Drain blocks
	// on this waitgroup so the agent process does not exit before pending
	// reaps complete.
	//
	// (CW-G3 Change 1).
	activeRunners sync.WaitGroup
}

// NewDispatcher constructs a Dispatcher bound to conn.
func NewDispatcher(conn *ipc.Conn) *Dispatcher {
	return &Dispatcher{conn: conn, runner: NewRunner()}
}

// currentConn returns the dispatcher's current IPC conn under read lock.
// Consumers (SendControl) must use this rather than reading d.conn directly
// because CW-G4 swaps the conn on reattach (AdoptConn).
func (d *Dispatcher) currentConn() *ipc.Conn {
	d.connMu.RLock()
	defer d.connMu.RUnlock()
	return d.conn
}

// SetPersistentShutdown wires the persistent-mode shutdown channel.
// When set (persistent=true), the dispatcher closes shutdownReq after
// the child terminates (Stop or natural exit), allowing agent.Run to
// fall through to cleanup before the bootstrap socketpair would EOF.
//
// CW-G4 Task 15+16. Called once at agent.Run startup when in persistent
// mode; safe to call before or after the dispatcher has begun handling
// IPC messages.
func (d *Dispatcher) SetPersistentShutdown(ch chan struct{}) {
	d.shutdownMu.Lock()
	defer d.shutdownMu.Unlock()
	d.persistent = true
	d.shutdownReq = ch
}

// persistentSessionDir, when set, enables CW-G4 meta.json updates on
// StartChild — the full Spec (Command, Args, PTY config, etc.) is recorded
// so reattach hosts can reconstruct it. Required because initPersistent
// runs BEFORE StartChild arrives, so the initial meta.json only has the
// agent ID.
func (d *Dispatcher) SetPersistentSessionDir(dir string) {
	d.persistentSessionDir = dir
}

// requestPersistentShutdown closes the shutdownReq channel exactly once.
// No-op when not persistent or when shutdownReq is nil.
func (d *Dispatcher) requestPersistentShutdown() {
	d.shutdownMu.Lock()
	defer d.shutdownMu.Unlock()
	if !d.persistent || d.shutdownReq == nil {
		return
	}
	select {
	case <-d.shutdownReq:
		// already closed
	default:
		close(d.shutdownReq)
	}
}

// AdoptConn replaces the dispatcher's current conn with newConn. Used by
// the persistent agent's accept-loop on reattach: after sending Hello +
// PTYRingDump on the freshly accepted socket, the agent rewires d.conn so
// all subsequent inbound messages route through it and outbound messages
// (PTY data, child status, etc.) go to the new host.
//
// The previously-adopted conn is closed before the new one is installed.
// Callers must call newConn.OnMessage(d.Handle) and newConn.Start() before
// or after AdoptConn — both orderings are safe; OnMessage is atomic.
//
// CW-G4 Task 12e.
func (d *Dispatcher) AdoptConn(newConn *ipc.Conn) {
	d.connMu.Lock()
	old := d.conn
	d.conn = newConn
	d.connMu.Unlock()

	// Close the old conn so its read/write loops exit and resources release.
	// Use a short ctx — the old conn is dead from the host's perspective and
	// we just need its goroutines to wind down.
	if old != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		_ = old.Close(closeCtx)
		cancel()
	}
}

// Handle processes an inbound message from the host.
func (d *Dispatcher) Handle(msg ipc.OutboxMessage) {
	if os.Getenv("CLIWRAP_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "AGENT: Handle msgType=%d\n", msg.Header.MsgType)
	}
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
	case ipc.MsgTypePTYWrite:
		w, err := ipc.DecodePTYWrite(msg.Payload)
		if err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "pty_write", Message: err.Error()}, false)
			return
		}
		if err := d.runner.WriteToActivePTY(w.Bytes); err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "pty_write", Message: err.Error()}, false)
		}
	case ipc.MsgTypePTYResize:
		rz, err := ipc.DecodePTYResize(msg.Payload)
		if err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "pty_resize", Message: err.Error()}, false)
			return
		}
		if err := d.runner.ResizeActivePTY(rz.Cols, rz.Rows); err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "pty_resize", Message: err.Error()}, false)
		}
	case ipc.MsgTypePTYSignal:
		sg, err := ipc.DecodePTYSignal(msg.Payload)
		if err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "pty_signal", Message: err.Error()}, false)
			return
		}
		if err := d.runner.SignalActivePTY(syscall.Signal(sg.Signum)); err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "pty_signal", Message: err.Error()}, false)
		}
	case ipc.MsgTypeCapabilityQuery:
		// CLIWRAP_AGENT_NO_CAPABILITY=1 skips the reply, simulating an older
		// agent that does not know this message type (test-only escape hatch
		// for Task 19).
		if os.Getenv("CLIWRAP_AGENT_NO_CAPABILITY") == "1" {
			return
		}
		reply := BuildCapabilityReply()
		_ = d.SendControl(ipc.MsgTypeCapabilityReply, reply, false)
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

	// CW-G4: when persistent, update meta.json with the full spec from
	// StartChild so reattach hosts can reconstruct PTY config, command,
	// etc. The initial meta.json (written by initPersistent) only has
	// the agent ID — too sparse for ProcessHandle reconstruction.
	if d.persistent && d.persistentSessionDir != "" {
		if existing, err := ReadPersistentMeta(d.persistentSessionDir); err == nil {
			fullSpec := existing.Spec
			fullSpec.Command = p.Command
			fullSpec.Args = append([]string(nil), p.Args...)
			fullSpec.Env = p.Env
			fullSpec.WorkDir = p.WorkDir
			fullSpec.StopTimeout = time.Duration(p.StopTimeout) * time.Millisecond
			if p.PTY != nil {
				fullSpec.PTY = &cwtypes.PTYConfig{
					InitialCols: p.PTY.InitialCols,
					InitialRows: p.PTY.InitialRows,
					Echo:        p.PTY.Echo,
				}
			}
			existing.Spec = fullSpec
			_ = WritePersistentMeta(d.persistentSessionDir, existing)
		}
	}

	var ptyCfg *cwtypes.PTYConfig
	if p.PTY != nil {
		ptyCfg = &cwtypes.PTYConfig{
			InitialCols: p.PTY.InitialCols,
			InitialRows: p.PTY.InitialRows,
			Echo:        p.PTY.Echo,
		}
	}

	d.wg.Add(1)
	// CW-G3: register this runner with Drain's waitgroup so agent.Run
	// blocks on its completion (cmd.Wait + child reap) before letting
	// the agent process exit. release() is sync.Once-protected; deferred
	// inside the goroutine so any return path decrements correctly.
	release := d.markRunnerActive()
	go func() {
		defer release()
		defer d.wg.Done()
		res, err := d.runner.Run(ctx, RunSpec{
			Command:     p.Command,
			Args:        p.Args,
			Env:         p.Env,
			WorkDir:     p.WorkDir,
			StopTimeout: time.Duration(p.StopTimeout) * time.Millisecond,
			PTY:         ptyCfg,
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
			// CW-G4: a persistent agent's child failed to exec — no work
			// remains, signal self-shutdown.
			d.requestPersistentShutdown()
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
		// CW-G4: persistent agent terminates when child terminates.
		// Whether the exit was from h.Stop, signal, or natural completion,
		// there is no more work for the supervisor.
		d.requestPersistentShutdown()
	}()
}

// stopChild cancels the running child and waits for the runner goroutine to
// exit. The timeout parameter is part of the wire protocol but is enforced
// inside Runner via RunSpec.StopTimeout, not here: cancel() triggers the
// SIGTERM→SIGKILL escalation, after which Runner.Run returns and wg.Done()
// fires. wg.Wait() is therefore bounded by StopTimeout.
//gocritic:ignore
func (d *Dispatcher) stopChild(_ time.Duration) {
	if os.Getenv("CLIWRAP_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "AGENT: stopChild entered\n")
	}
	d.mu.Lock()
	cancel := d.cancelFn
	d.mu.Unlock()
	if cancel == nil {
		if os.Getenv("CLIWRAP_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "AGENT: stopChild cancel=nil, returning\n")
		}
		return
	}
	cancel()
	if os.Getenv("CLIWRAP_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "AGENT: stopChild called cancel, waiting on wg\n")
	}
	d.wg.Wait()
	if os.Getenv("CLIWRAP_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "AGENT: stopChild wg.Wait done\n")
	}
}

func (d *Dispatcher) clearRunning() {
	d.mu.Lock()
	d.cancelFn = nil
	d.runningPID = 0
	d.mu.Unlock()
}

// installLogSinks replaces the runner's default io.Discard sinks with
// chunkWriter instances that forward bytes to the host via MsgLogChunk.
// It also configures the runner's PTY sender so MsgTypePTYData frames are
// routed through the same IPC path.
// Extracted from startChild so it can be unit-tested without a real Conn.
func (d *Dispatcher) installLogSinks(sender chunkSender) {
	d.runner.StdoutSink = newChunkWriter(sender, 0) // 0 = stdout
	d.runner.StderrSink = newChunkWriter(sender, 1) // 1 = stderr
	d.runner.SetSender(sender)
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
		SeqNo:   d.currentConn().Seqs().Next(),
		Length:  uint32(len(data)),
	}
	if ackRequired {
		h.Flags |= ipc.FlagAckRequired
	}
	d.currentConn().Send(ipc.OutboxMessage{Header: h, Payload: data})
	return nil
}

func ctxWithTimeoutSeconds(n int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
}

// markRunnerActive registers an in-flight runner goroutine with the dispatcher
// and returns a release closure that the goroutine must call (typically via
// defer) AFTER cmd.Wait() returns. The closure is sync.Once-protected, so
// calling it more than once is a no-op rather than a double-decrement panic.
//
// Drain blocks on the resulting waitgroup. See CW-G3 spec
// Change 1) for the contract: agent.Run must not return until every runner
// has reaped its child, otherwise the host observes a "process leak" while
// the kernel still holds zombies pending wait4.
func (d *Dispatcher) markRunnerActive() func() {
	d.activeRunners.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() { d.activeRunners.Done() })
	}
}

// Drain blocks until all goroutines registered via markRunnerActive have
// released their tokens, or until ctx is canceled / its deadline elapses.
// Returns nil on clean drain, ctx.Err() otherwise.
//
// Intended caller: agent.Run, after the IPC connection has begun shutting
// down. See CW-G3 spec.
func (d *Dispatcher) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		d.activeRunners.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
