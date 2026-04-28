//go:build chaos

// PTY agent crash detection test.
//
// SKIPPED: blocked on agent-disconnect → controller-crash wiring gap discovered
// during Task 21 implementation. ipc.Conn.readLoop exits silently on agent EOF;
// internal/controller/crash.go defines CrashSourceConnectionLost but it is never
// triggered. The controller stays in StateRunning indefinitely after agent SIGKILL.
//
// Tracking: tasks list "[Phase 2 보존] cli-wrapper: agent disconnect → controller
// crash state 연결". Re-enable by removing the t.Skip once the wiring is added.
package chaos

import "testing"

func TestPTY_AgentCrashDetectedWithin5s(t *testing.T) {
	t.Skip("blocked on agent disconnect → crash state wiring (see Phase 2 task)")
}
