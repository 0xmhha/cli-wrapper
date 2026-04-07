package platform

import (
	"os"
	"os/exec"
)

// Platform abstracts OS-specific operations so the rest of the code base
// never imports syscall or os directly for process management.
type Platform interface {
	// FindExecutable locates cmd using $PATH semantics.
	FindExecutable(cmd string) (string, error)

	// SendSignal delivers sig to pid. Returns an error if the process
	// does not exist or the caller lacks permission.
	SendSignal(pid int, sig os.Signal) error

	// Kill is shorthand for SendSignal(pid, SIGKILL).
	Kill(pid int) error
}

// Default returns the Platform implementation for the current OS.
func Default() Platform { return defaultImpl }

type basePlatform struct{}

func (basePlatform) FindExecutable(cmd string) (string, error) {
	return exec.LookPath(cmd)
}

func (basePlatform) SendSignal(pid int, sig os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

func (p basePlatform) Kill(pid int) error {
	return p.SendSignal(pid, os.Kill)
}
