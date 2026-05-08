// SPDX-License-Identifier: Apache-2.0

//go:build windows

package platform

// On Windows the basePlatform helpers (exec.LookPath, os.Process.Signal,
// os.Kill) are sufficient for the operations the platform.Platform interface
// currently exposes. Process-group / signal-cascade primitives that do NOT
// have a Windows equivalent (SIGTERM-the-whole-pgrp, etc.) are handled at
// higher layers (supervise.Spawner, agent/runner) which are themselves
// gated by !windows build tags until the Windows runtime port lands.
var defaultImpl Platform = basePlatform{}
