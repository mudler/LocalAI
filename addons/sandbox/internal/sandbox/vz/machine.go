package vz

import (
	"context"
	"net"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
)

// machine is the part of a VM that the backend uses. *vmm.VM is the real
// one. The tests use a fake.
type machine interface {
	// Start boots the machine.
	Start(ctx context.Context) error
	// Listen calls accept for each connection that the guest opens to a host
	// port. accept must not block.
	Listen(port uint32, accept func(net.Conn)) error
	// Unlisten removes the listener of a port. The machine then refuses new
	// connections to it. It must not be called from accept.
	Unlisten(port uint32)
	// Dial opens a connection to a port that the guest listens on.
	Dial(ctx context.Context, port uint32) (net.Conn, error)
	// Done is closed when the machine has stopped.
	Done() <-chan struct{}
	// Stop ends the machine at once and waits for it.
	Stop()
}

// machineFactory makes a machine from its description.
type machineFactory func(c vmm.Config) (machine, error)
