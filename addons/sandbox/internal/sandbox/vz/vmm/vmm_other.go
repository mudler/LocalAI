//go:build !(darwin && arm64)

package vmm

import (
	"context"
	"errors"
	"net"
)

// ErrUnsupported says that this system cannot run a VM with
// Virtualization.framework.
var ErrUnsupported = errors.New("vmm: Virtualization.framework needs macOS on Apple silicon")

// Config describes one machine.
type Config struct {
	Kernel      string // uncompressed arm64 kernel image
	Cmdline     string // kernel command line
	Root        string // read-only raw disk image
	Upper       string // read-write raw disk image
	Lower       string // folder shared read-only with the tag "lower"
	Console     string // file that the console log is appended to
	CPUs        uint
	MemoryBytes uint64
}

// VM is one machine.
type VM struct{}

// HostLimits are the smallest and the largest VM that the framework accepts
// on this Mac.
type HostLimits struct {
	MinCPUs, MaxCPUs     uint
	MinMemory, MaxMemory uint64 // bytes
}

// Limits returns zero limits on this system.
func Limits() HostLimits { return HostLimits{} }

// Check reports why this host cannot run a VM, or nil.
func Check() error { return ErrUnsupported }

// New always fails on this system.
func New(Config) (*VM, error) { return nil, ErrUnsupported }

// Start always fails on this system.
func (*VM) Start(context.Context) error { return ErrUnsupported }

// Listen always fails on this system.
func (*VM) Listen(uint32, func(net.Conn)) error { return ErrUnsupported }

// Dial always fails on this system.
func (*VM) Dial(context.Context, uint32) (net.Conn, error) { return nil, ErrUnsupported }

// Done returns a channel that is already closed.
func (*VM) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// Stop does nothing on this system.
func (*VM) Stop() {}
