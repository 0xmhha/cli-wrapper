// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package cgroup

// Group is a stub on non-Linux platforms. Every method returns
// ErrUnsupportedPlatform so callers can detect cgroup-unavailable hosts
// without conditional compilation in their own code.
type Group struct {
	Path string
}

// New always returns ErrUnsupportedPlatform on non-Linux.
func New(parent, name string) (*Group, error) {
	return nil, ErrUnsupportedPlatform
}

func (g *Group) AddPID(pid int) error           { return ErrUnsupportedPlatform }
func (g *Group) SetCPUMax(m CPUMax) error       { return ErrUnsupportedPlatform }
func (g *Group) SetMemoryMax(m MemoryMax) error { return ErrUnsupportedPlatform }
func (g *Group) SetIOWeight(w IOWeight) error   { return ErrUnsupportedPlatform }
func (g *Group) Remove() error                  { return ErrUnsupportedPlatform }
