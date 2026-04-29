// SPDX-License-Identifier: Apache-2.0

package controller

import "os"

// AgentProcess returns the OS process of the running agent subprocess.
// Exposed for tests that need to force-kill the agent (e.g. disconnect tests).
// Production code must not use this.
func (c *Controller) AgentProcess() *os.Process {
	if c.handle == nil {
		return nil
	}
	return c.handle.Process
}

// ForceDisconnectForTest closes the host-side IPC socket without going through
// Conn.Close(), which would suppress the disconnect callback. This lets tests
// verify that the controller transitions to StateCrashed on an unexpected
// disconnect without having to kill the agent process.
// Production code must not use this.
func (c *Controller) ForceDisconnectForTest() error {
	if c.handle == nil {
		return nil
	}
	return c.handle.Socket.Close()
}
