//go:build darwin && arm64

package vmm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/virtualization"
)

// ErrUnsupported says that this system cannot run a VM with
// Virtualization.framework.
var ErrUnsupported = errors.New("vmm: Virtualization.framework is not available")

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

// VM is one machine. All methods are safe for concurrent use.
type VM struct {
	q    dispatch.Queue
	vm   virtualization.VZVirtualMachine
	sock virtualization.VZVirtioSocketDevice

	done     chan struct{}
	doneOnce sync.Once

	mu      sync.Mutex
	started bool
	// keep holds the Objective-C objects that the framework uses but does not
	// retain on our behalf (listeners and delegates).
	keep []any
	// listeners holds the objects of each listener, so that Unlisten can
	// release them.
	listeners map[uint32][]any
}

// Check reports why this host cannot run a VM, or nil. It fails when the
// framework is not available, or when the process lacks the
// com.apple.security.virtualization entitlement.
func Check() error {
	var supported bool
	var verr error
	q := dispatch.QueueCreate("vmm.check")
	q.Sync(func() {
		supported = virtualization.GetVZVirtualMachineClass().IsSupported()
		if !supported {
			return
		}
		// A configuration that the framework refuses for another reason
		// (this one has no real kernel) still shows a missing entitlement.
		bl := virtualization.NewLinuxBootLoaderWithKernelURL(fileURL("/dev/null"))
		cfg := virtualization.NewVZVirtualMachineConfiguration()
		cfg.SetBootLoader(bl)
		cfg.SetCPUCount(1)
		cfg.SetMemorySize(256 << 20)
		_, verr = cfg.ValidateWithError()
	})
	if !supported {
		return fmt.Errorf("%w: this Mac does not support virtualization", ErrUnsupported)
	}
	if verr != nil && strings.Contains(strings.ToLower(verr.Error()), "entitlement") {
		return fmt.Errorf("%w: the add-on is not signed with the com.apple.security.virtualization entitlement: %v", ErrUnsupported, verr)
	}
	return nil
}

// HostLimits are the smallest and the largest VM that the framework accepts
// on this Mac.
type HostLimits struct {
	MinCPUs, MaxCPUs     uint
	MinMemory, MaxMemory uint64 // bytes
}

// Limits returns the limits of this Mac.
func Limits() HostLimits {
	var l HostLimits
	q := dispatch.QueueCreate("vmm.limits")
	q.Sync(func() {
		c := virtualization.GetVZVirtualMachineConfigurationClass()
		l = HostLimits{c.MinimumAllowedCPUCount(), c.MaximumAllowedCPUCount(), c.MinimumAllowedMemorySize(), c.MaximumAllowedMemorySize()}
	})
	return l
}

func fileURL(p string) foundation.NSURL { return foundation.NewURLFileURLWithPath(p) }

// onQueue runs f on the serial queue of the VM and waits for it. It must not
// be called from a callback that already runs on that queue.
func (v *VM) onQueue(f func()) { v.q.Sync(f) }

// New builds a machine. It does not start it.
func New(c Config) (*VM, error) {
	if c.CPUs == 0 || c.MemoryBytes == 0 {
		return nil, errors.New("vmm: CPUs and MemoryBytes are required")
	}
	v := &VM{done: make(chan struct{}), q: dispatch.QueueCreate("localai.sandbox.vm")}
	var err error
	v.onQueue(func() {
		var cfg virtualization.VZVirtualMachineConfiguration
		if cfg, err = buildConfig(c); err != nil {
			return
		}
		v.vm = virtualization.NewVirtualMachineWithConfigurationQueue(cfg, v.q)
		devs := v.vm.SocketDevices()
		if len(devs) != 1 {
			err = fmt.Errorf("vmm: the machine has %d socket devices, want 1", len(devs))
			return
		}
		v.sock = virtualization.VZVirtioSocketDeviceFromID(devs[0].ID)
		d := virtualization.NewVZVirtualMachineDelegate(virtualization.VZVirtualMachineDelegateConfig{
			GuestDidStopVirtualMachine: func(virtualization.VZVirtualMachine) { v.finish() },
			VirtualMachineDidStopWithError: func(virtualization.VZVirtualMachine, foundation.NSError) {
				v.finish()
			},
		})
		v.vm.SetDelegate(d)
		v.keep = append(v.keep, d)
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

func buildConfig(c Config) (virtualization.VZVirtualMachineConfiguration, error) {
	var zero virtualization.VZVirtualMachineConfiguration
	bl := virtualization.NewLinuxBootLoaderWithKernelURL(fileURL(c.Kernel))
	bl.SetCommandLine(c.Cmdline)
	cfg := virtualization.NewVZVirtualMachineConfiguration()
	cfg.SetBootLoader(bl)
	cfg.SetCPUCount(c.CPUs)
	cfg.SetMemorySize(c.MemoryBytes)

	disk := func(path string, readOnly bool) (virtualization.VZStorageDeviceConfiguration, error) {
		// Full synchronization makes a flush of the guest reach the disk, so
		// a frozen file system that the host reads is complete.
		a, err := virtualization.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError(
			fileURL(path), readOnly, virtualization.VZDiskImageCachingModeAutomatic, virtualization.VZDiskImageSynchronizationModeFull)
		if err != nil {
			return virtualization.VZStorageDeviceConfiguration{}, fmt.Errorf("vmm: disk %s: %w", path, err)
		}
		d := virtualization.NewVirtioBlockDeviceConfigurationWithAttachment(a)
		return virtualization.VZStorageDeviceConfigurationFromID(d.ID), nil
	}
	root, err := disk(c.Root, true)
	if err != nil {
		return zero, err
	}
	upper, err := disk(c.Upper, false)
	if err != nil {
		return zero, err
	}
	cfg.SetStorageDevices([]virtualization.VZStorageDeviceConfiguration{root, upper})

	fs := virtualization.NewVirtioFileSystemDeviceConfigurationWithTag("lower")
	fs.SetShare(virtualization.NewSingleDirectoryShareWithDirectory(virtualization.NewSharedDirectoryWithURLReadOnly(fileURL(c.Lower), true)))
	cfg.SetDirectorySharingDevices([]virtualization.VZDirectorySharingDeviceConfiguration{virtualization.VZDirectorySharingDeviceConfigurationFromID(fs.ID)})

	vs := virtualization.NewVZVirtioSocketDeviceConfiguration()
	cfg.SetSocketDevices([]virtualization.VZSocketDeviceConfiguration{virtualization.VZSocketDeviceConfigurationFromID(vs.ID)})

	sa, err := virtualization.NewFileSerialPortAttachmentWithURLAppendError(fileURL(c.Console), false)
	if err != nil {
		return zero, fmt.Errorf("vmm: console %s: %w", c.Console, err)
	}
	sp := virtualization.NewVZVirtioConsoleDeviceSerialPortConfiguration()
	sp.SetAttachment(sa)
	cfg.SetSerialPorts([]virtualization.VZSerialPortConfiguration{virtualization.VZSerialPortConfigurationFromID(sp.ID)})

	ent := virtualization.NewVZVirtioEntropyDeviceConfiguration()
	cfg.SetEntropyDevices([]virtualization.VZEntropyDeviceConfiguration{virtualization.VZEntropyDeviceConfigurationFromID(ent.ID)})

	if _, err := cfg.ValidateWithError(); err != nil {
		return zero, fmt.Errorf("vmm: invalid machine: %w", err)
	}
	return cfg, nil
}

// finish closes Done. It is safe to call more than once.
func (v *VM) finish() { v.doneOnce.Do(func() { close(v.done) }) }

// Done is closed when the machine has stopped for any reason: the guest
// powered off, the framework reported an error, or Stop ran.
func (v *VM) Done() <-chan struct{} { return v.done }

// Start boots the machine and returns when the framework says it runs, not
// when the guest is ready.
func (v *VM) Start(ctx context.Context) error {
	res := make(chan error, 1)
	v.mu.Lock()
	if v.started {
		v.mu.Unlock()
		return errors.New("vmm: the machine was started already")
	}
	v.started = true
	// Queue the start while holding the lock, so that a Stop that reads
	// started=true is queued behind it.
	v.q.Async(func() {
		v.vm.StartWithCompletionHandler(func(err error) { res <- err })
	})
	v.mu.Unlock()
	select {
	case err := <-res:
		if err != nil {
			v.finish()
			return fmt.Errorf("vmm: start: %w", err)
		}
	case <-ctx.Done():
		// The framework cannot stop a machine that is still starting, and
		// the start goes on after the context ends. Wait for it (Stop does
		// that too), then stop the machine, so that no VM runs untracked
		// and keeps the disk images open.
		select {
		case <-res:
		case <-time.After(stopTimeout):
		}
		v.Stop()
		return ctx.Err()
	}
	go v.poll()
	return nil
}

// poll closes Done when the framework state shows a stopped machine and no
// delegate call said so.
func (v *VM) poll() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-v.done:
			return
		case <-t.C:
			var st virtualization.VZVirtualMachineState
			v.onQueue(func() { st = v.vm.State() })
			if st == virtualization.VZVirtualMachineStateStopped || st == virtualization.VZVirtualMachineStateError {
				v.finish()
				return
			}
		}
	}
}

// stopTimeout bounds how long Stop waits for a machine that is not in a state
// from which it can stop, and for the stop itself.
const stopTimeout = 30 * time.Second

// Stop ends the machine at once, as if the power was cut, and returns when it
// is stopped. It is safe to call more than once and on a machine that never
// started. The framework refuses to stop a machine in some states (for
// example while it starts), so Stop tries again until the machine has
// stopped or stopTimeout has passed. Done closes only when the framework
// state is stopped or error: after a timeout Stop logs and returns with the
// machine still running, and Done stays open.
func (v *VM) Stop() {
	select {
	case <-v.done:
		return
	default:
	}
	v.mu.Lock()
	started := v.started
	v.mu.Unlock()
	if !started {
		v.finish()
		return
	}
	deadline := time.Now().Add(stopTimeout)
	var lastErr error
	for {
		var st virtualization.VZVirtualMachineState
		res := make(chan error, 1)
		asked := false
		v.onQueue(func() {
			st = v.vm.State()
			if st == virtualization.VZVirtualMachineStateStopped || st == virtualization.VZVirtualMachineStateError || !v.vm.CanStop() {
				return
			}
			asked = true
			v.vm.StopWithCompletionHandler(func(err error) { res <- err })
		})
		if st == virtualization.VZVirtualMachineStateStopped || st == virtualization.VZVirtualMachineStateError {
			v.finish()
			return
		}
		if asked {
			select {
			case err := <-res:
				if err != nil {
					lastErr = err
					log.Printf("vmm: stop: %v", err)
				}
			case <-time.After(10 * time.Second):
				lastErr = errors.New("the stop did not complete in 10 s")
				log.Printf("vmm: stop: %v", lastErr)
			}
		}
		if !time.Now().Before(deadline) {
			log.Printf("vmm: the machine did not stop: state %d, last error %v", st, lastErr)
			return
		}
		if !asked {
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// Listen calls accept for each connection that the guest opens to the host
// port. accept runs on the queue of the VM, so it must not block: hand the
// connection to a goroutine.
func (v *VM) Listen(port uint32, accept func(net.Conn)) error {
	v.onQueue(func() {
		d := virtualization.NewVZVirtioSocketListenerDelegate(virtualization.VZVirtioSocketListenerDelegateConfig{
			ListenerShouldAcceptNewConnectionFromSocketDevice: func(_ virtualization.VZVirtioSocketListener, c virtualization.VZVirtioSocketConnection, _ virtualization.VZVirtioSocketDevice) bool {
				nc, cerr := connOf(c)
				if cerr != nil {
					return false
				}
				accept(nc)
				return true
			},
		})
		l := virtualization.NewVZVirtioSocketListener()
		l.SetDelegate(d)
		v.sock.SetSocketListenerForPort(l, port)
		v.mu.Lock()
		if v.listeners == nil {
			v.listeners = map[uint32][]any{}
		}
		v.listeners[port] = []any{l, d}
		v.mu.Unlock()
	})
	return nil
}

// Unlisten removes the listener of a port. The framework refuses every new
// connection of the guest to it. Connections that Listen already handed out
// stay open. Dial is not affected: it opens connections to the guest.
// Unlisten must not be called from accept, which runs on the queue of the VM.
func (v *VM) Unlisten(port uint32) {
	v.onQueue(func() {
		v.mu.Lock()
		_, ok := v.listeners[port]
		delete(v.listeners, port)
		v.mu.Unlock()
		if ok {
			v.sock.RemoveSocketListenerForPort(port)
		}
	})
}

// Dial opens a connection to a port that the guest listens on. It returns an
// error when nothing listens there.
func (v *VM) Dial(ctx context.Context, port uint32) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	res := make(chan result, 1)
	v.q.Async(func() {
		v.sock.ConnectToPortCompletionHandler(port, func(c *virtualization.VZVirtioSocketConnection, err error) {
			if err != nil {
				res <- result{err: fmt.Errorf("vmm: connect to port %d: %w", port, err)}
				return
			}
			nc, cerr := connOf(*c)
			res <- result{nc, cerr}
		})
	})
	select {
	case r := <-res:
		return r.c, r.err
	case <-ctx.Done():
		// The connection that may still arrive is closed when it does.
		go func() {
			if r := <-res; r.c != nil {
				_ = r.c.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// connOf turns a framework connection into a net.Conn that this process owns.
// The framework closes its own descriptor when the connection object goes, so
// this function duplicates it first and closes the original at once.
func connOf(c virtualization.VZVirtioSocketConnection) (net.Conn, error) {
	fd, err := syscall.Dup(int(c.FileDescriptor()))
	c.Close()
	if err != nil {
		return nil, fmt.Errorf("vmm: duplicate the connection: %w", err)
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "vsock")
	defer func() { _ = f.Close() }()
	return net.FileConn(f)
}
