//go:build darwin && arm64

package vmm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
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
//
// The Objective-C objects of a VM (the machine, its dispatch queue and the
// socket listeners) are owned by this struct. release gives them back once
// the machine has stopped, and it runs on the queue of the VM, so that no
// callback can use an object while it goes. Every use of an object on the
// queue checks released first.
type VM struct {
	q    dispatch.Queue
	vm   virtualization.VZVirtualMachine
	sock virtualization.VZVirtioSocketDevice

	done     chan struct{}
	doneOnce sync.Once

	// relOnce makes release run once.
	relOnce sync.Once
	// released is set on the queue by release. It is read on the queue only.
	released bool
	// qmu guards qusers and qgone: the callers that are about to use the
	// queue, or that wait for it, and the end of the queue. release frees the
	// queue only when no caller uses it, and a later caller gets false from
	// onQueue or async instead of a queue that is gone.
	qmu    sync.Mutex
	qcond  *sync.Cond
	qusers int
	qgone  bool

	mu      sync.Mutex
	started bool
	// listeners holds the listener of each port. The framework keeps its
	// listeners, and the VM owns one reference to each.
	listeners map[uint32]virtualization.VZVirtioSocketListener
}

// The framework calls delegates of one class for every machine, so the
// process registers its delegate classes once and the callbacks find the
// machine, or the accept function, by the identity of the object that the
// framework passes in. A class is never freed, and each of its methods is a
// native callback of which purego allows about 2000 for the whole process.
var hooks struct {
	once       sync.Once
	vmDelegate virtualization.VZVirtualMachineDelegateObject
	lnDelegate virtualization.VZVirtioSocketListenerDelegateObject

	mu       sync.Mutex
	machines map[objc.ID]*VM            // by the VZVirtualMachine
	accepts  map[objc.ID]func(net.Conn) // by the VZVirtioSocketListener
}

func initHooks() {
	hooks.once.Do(func() {
		hooks.machines = map[objc.ID]*VM{}
		hooks.accepts = map[objc.ID]func(net.Conn){}
		stopped := func(vm virtualization.VZVirtualMachine) {
			hooks.mu.Lock()
			m := hooks.machines[vm.ID]
			hooks.mu.Unlock()
			if m != nil {
				m.finish()
			}
		}
		hooks.vmDelegate = virtualization.NewVZVirtualMachineDelegate(virtualization.VZVirtualMachineDelegateConfig{
			GuestDidStopVirtualMachine: stopped,
			VirtualMachineDidStopWithError: func(vm virtualization.VZVirtualMachine, _ foundation.NSError) {
				stopped(vm)
			},
		})
		hooks.lnDelegate = virtualization.NewVZVirtioSocketListenerDelegate(virtualization.VZVirtioSocketListenerDelegateConfig{
			ListenerShouldAcceptNewConnectionFromSocketDevice: func(l virtualization.VZVirtioSocketListener, c virtualization.VZVirtioSocketConnection, _ virtualization.VZVirtioSocketDevice) bool {
				hooks.mu.Lock()
				accept := hooks.accepts[l.ID]
				hooks.mu.Unlock()
				if accept == nil {
					return false
				}
				nc, cerr := connOf(c)
				if cerr != nil {
					return false
				}
				accept(nc)
				return true
			},
		})
	})
}

// release sends release to an Objective-C object that this code owns. It
// ignores a nil object.
func release(id objc.ID) {
	if id != 0 {
		objc.Send[objc.ID](id, objc.Sel("release"))
	}
}

// pooled runs f in an autorelease pool. The framework returns many objects
// (errors, arrays, strings) that it autoreleases. A Go thread has no pool, so
// without one they stay in memory for good.
func pooled(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p := foundation.NewNSAutoreleasePool()
	defer p.Drain()
	f()
}

// Check reports why this host cannot run a VM, or nil. It fails when the
// framework is not available, or when the process lacks the
// com.apple.security.virtualization entitlement.
func Check() error {
	var supported bool
	var verr error
	q := dispatch.QueueCreate("vmm.check")
	defer release(q.ID())
	q.Sync(func() {
		pooled(func() {
			supported = virtualization.GetVZVirtualMachineClass().IsSupported()
			if !supported {
				return
			}
			// A configuration that the framework refuses for another reason
			// (this one has no real kernel) still shows a missing entitlement.
			url := fileURL("/dev/null")
			bl := virtualization.NewLinuxBootLoaderWithKernelURL(url)
			cfg := virtualization.NewVZVirtualMachineConfiguration()
			defer release(url.ID)
			defer release(bl.ID)
			defer release(cfg.ID)
			cfg.SetBootLoader(bl)
			cfg.SetCPUCount(1)
			cfg.SetMemorySize(256 << 20)
			_, verr = cfg.ValidateWithError()
		})
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
	defer release(q.ID())
	q.Sync(func() {
		pooled(func() {
			c := virtualization.GetVZVirtualMachineConfigurationClass()
			l = HostLimits{c.MinimumAllowedCPUCount(), c.MaximumAllowedCPUCount(), c.MinimumAllowedMemorySize(), c.MaximumAllowedMemorySize()}
		})
	})
	return l
}

// fileURL returns a URL that the caller owns and must release.
func fileURL(p string) foundation.NSURL { return foundation.NewURLFileURLWithPath(p) }

// acquire says that the caller is about to use the queue. It returns false
// when release has freed the queue. A caller that got true calls drop.
func (v *VM) acquire() bool {
	v.qmu.Lock()
	defer v.qmu.Unlock()
	if v.qgone {
		return false
	}
	v.qusers++
	return true
}

func (v *VM) drop() {
	v.qmu.Lock()
	v.qusers--
	if v.qusers == 0 {
		v.qcond.Broadcast()
	}
	v.qmu.Unlock()
}

// onQueue runs f on the serial queue of the VM and waits for it. It must not
// be called from a callback that already runs on that queue. It returns false,
// and does not run f, when the VM gave its queue back: the VM has stopped.
func (v *VM) onQueue(f func()) bool {
	if !v.acquire() {
		return false
	}
	defer v.drop()
	v.q.Sync(f)
	return true
}

// async queues f on the queue of the VM and does not wait. It returns false,
// and does not queue f, when the VM gave its queue back. A queued f keeps the
// queue alive until it has run.
func (v *VM) async(f func()) bool {
	if !v.acquire() {
		return false
	}
	defer v.drop()
	v.q.Async(f)
	return true
}

// New builds a machine. It does not start it. The machine owns native
// objects until it has stopped: Stop, or a guest that ends by itself, gives
// them back.
func New(c Config) (*VM, error) {
	if c.CPUs == 0 || c.MemoryBytes == 0 {
		return nil, errors.New("vmm: CPUs and MemoryBytes are required")
	}
	initHooks()
	v := &VM{done: make(chan struct{}), q: dispatch.QueueCreate("localai.sandbox.vm")}
	v.qcond = sync.NewCond(&v.qmu)
	var err error
	v.onQueue(func() {
		pooled(func() {
			var cfg virtualization.VZVirtualMachineConfiguration
			if cfg, err = buildConfig(c); err != nil {
				return
			}
			// The machine keeps a copy of the configuration.
			defer release(cfg.ID)
			v.vm = virtualization.NewVirtualMachineWithConfigurationQueue(cfg, v.q)
			devs := v.vm.SocketDevices()
			if len(devs) != 1 {
				err = fmt.Errorf("vmm: the machine has %d socket devices, want 1", len(devs))
				release(v.vm.ID)
				v.vm = virtualization.VZVirtualMachine{}
				return
			}
			v.sock = virtualization.VZVirtioSocketDeviceFromID(devs[0].ID)
			hooks.mu.Lock()
			hooks.machines[v.vm.ID] = v
			hooks.mu.Unlock()
			v.vm.SetDelegate(hooks.vmDelegate)
		})
	})
	if err != nil {
		release(v.q.ID())
		return nil, err
	}
	// The machine gives its objects back once it has stopped, whoever
	// stops it.
	go func() {
		<-v.done
		v.release()
	}()
	return v, nil
}

// release gives back the native objects of the machine. It runs once, on the
// queue, after the machine has stopped: the framework may use the objects of
// a machine that runs. A call that comes after it finds released set and
// does not touch them.
func (v *VM) release() {
	v.relOnce.Do(func() {
		v.onQueue(func() {
			pooled(func() {
				v.released = true
				v.mu.Lock()
				ls := v.listeners
				v.listeners = nil
				v.mu.Unlock()
				hooks.mu.Lock()
				for _, l := range ls {
					delete(hooks.accepts, l.ID)
				}
				delete(hooks.machines, v.vm.ID)
				hooks.mu.Unlock()
				for _, l := range ls {
					release(l.ID)
				}
				release(v.vm.ID)
			})
		})
		// No caller may be on its way to the queue. The machine retains the
		// queue while it lives, and the queue its pending blocks.
		v.qmu.Lock()
		v.qgone = true
		for v.qusers > 0 {
			v.qcond.Wait()
		}
		v.qmu.Unlock()
		release(v.q.ID())
	})
}

// buildConfig returns a configuration that the caller owns and must release.
// The objects that it created for the parts are released here: the
// configuration keeps its own references.
func buildConfig(c Config) (cfg virtualization.VZVirtualMachineConfiguration, err error) {
	var owned []objc.ID
	own := func(id objc.ID) { owned = append(owned, id) }
	defer func() {
		for _, id := range owned {
			release(id)
		}
		if err != nil {
			release(cfg.ID)
			cfg = virtualization.VZVirtualMachineConfiguration{}
		}
	}()
	kurl := fileURL(c.Kernel)
	own(kurl.ID)
	bl := virtualization.NewLinuxBootLoaderWithKernelURL(kurl)
	own(bl.ID)
	bl.SetCommandLine(c.Cmdline)
	cfg = virtualization.NewVZVirtualMachineConfiguration()
	cfg.SetBootLoader(bl)
	cfg.SetCPUCount(c.CPUs)
	cfg.SetMemorySize(c.MemoryBytes)

	disk := func(path string, readOnly bool) (virtualization.VZStorageDeviceConfiguration, error) {
		u := fileURL(path)
		own(u.ID)
		// Full synchronization makes a flush of the guest reach the disk, so
		// a frozen file system that the host reads is complete.
		a, err := virtualization.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError(
			u, readOnly, virtualization.VZDiskImageCachingModeAutomatic, virtualization.VZDiskImageSynchronizationModeFull)
		if err != nil {
			return virtualization.VZStorageDeviceConfiguration{}, fmt.Errorf("vmm: disk %s: %w", path, err)
		}
		own(a.ID)
		d := virtualization.NewVirtioBlockDeviceConfigurationWithAttachment(a)
		own(d.ID)
		return virtualization.VZStorageDeviceConfigurationFromID(d.ID), nil
	}
	root, err := disk(c.Root, true)
	if err != nil {
		return cfg, err
	}
	upper, err := disk(c.Upper, false)
	if err != nil {
		return cfg, err
	}
	cfg.SetStorageDevices([]virtualization.VZStorageDeviceConfiguration{root, upper})

	lurl := fileURL(c.Lower)
	own(lurl.ID)
	sd := virtualization.NewSharedDirectoryWithURLReadOnly(lurl, true)
	own(sd.ID)
	share := virtualization.NewSingleDirectoryShareWithDirectory(sd)
	own(share.ID)
	fs := virtualization.NewVirtioFileSystemDeviceConfigurationWithTag("lower")
	own(fs.ID)
	fs.SetShare(share)
	cfg.SetDirectorySharingDevices([]virtualization.VZDirectorySharingDeviceConfiguration{virtualization.VZDirectorySharingDeviceConfigurationFromID(fs.ID)})

	vs := virtualization.NewVZVirtioSocketDeviceConfiguration()
	own(vs.ID)
	cfg.SetSocketDevices([]virtualization.VZSocketDeviceConfiguration{virtualization.VZSocketDeviceConfigurationFromID(vs.ID)})

	curl := fileURL(c.Console)
	own(curl.ID)
	sa, err := virtualization.NewFileSerialPortAttachmentWithURLAppendError(curl, false)
	if err != nil {
		return cfg, fmt.Errorf("vmm: console %s: %w", c.Console, err)
	}
	own(sa.ID)
	sp := virtualization.NewVZVirtioConsoleDeviceSerialPortConfiguration()
	own(sp.ID)
	sp.SetAttachment(sa)
	cfg.SetSerialPorts([]virtualization.VZSerialPortConfiguration{virtualization.VZSerialPortConfigurationFromID(sp.ID)})

	ent := virtualization.NewVZVirtioEntropyDeviceConfiguration()
	own(ent.ID)
	cfg.SetEntropyDevices([]virtualization.VZEntropyDeviceConfiguration{virtualization.VZEntropyDeviceConfigurationFromID(ent.ID)})

	if _, err := cfg.ValidateWithError(); err != nil {
		return cfg, fmt.Errorf("vmm: invalid machine: %w", err)
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
	if !v.async(func() {
		if v.released {
			res <- errReleased
			return
		}
		sendWithErrorBlock(v.vm.ID, "startWithCompletionHandler:", func(err error) { res <- err })
	}) {
		res <- errReleased
	}
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

// errReleased is the error of a call on a machine that has stopped and given
// its objects back.
var errReleased = errors.New("vmm: the machine has stopped")

// sendWithErrorBlock sends a message that takes a completion handler with an
// error. The framework copies the block, so this function drops its own
// reference to it at once. It must run on the queue of the machine.
func sendWithErrorBlock(id objc.ID, sel string, handler func(error)) {
	block, cleanup := virtualization.NewErrorBlock(handler)
	defer cleanup()
	objc.Send[objc.ID](id, objc.Sel(sel), block)
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
			if !v.onQueue(func() {
				if v.released {
					st = virtualization.VZVirtualMachineStateStopped
					return
				}
				pooled(func() { st = v.vm.State() })
			}) {
				st = virtualization.VZVirtualMachineStateStopped
			}
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
	// Wait for the release of the objects, so that nothing of a stopped
	// machine is left when Stop returns.
	defer func() {
		select {
		case <-v.done:
			v.release()
		default:
		}
	}()
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
		if !v.onQueue(func() {
			if v.released {
				st = virtualization.VZVirtualMachineStateStopped
				return
			}
			pooled(func() {
				st = v.vm.State()
				if st == virtualization.VZVirtualMachineStateStopped || st == virtualization.VZVirtualMachineStateError || !v.vm.CanStop() {
					return
				}
				asked = true
				sendWithErrorBlock(v.vm.ID, "stopWithCompletionHandler:", func(err error) { res <- err })
			})
		}) {
			st = virtualization.VZVirtualMachineStateStopped
		}
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
// connection to a goroutine. A second Listen on a port replaces the first.
func (v *VM) Listen(port uint32, accept func(net.Conn)) error {
	initHooks()
	var err error
	if !v.onQueue(func() {
		if v.released {
			err = errReleased
			return
		}
		pooled(func() {
			l := virtualization.NewVZVirtioSocketListener()
			hooks.mu.Lock()
			hooks.accepts[l.ID] = accept
			hooks.mu.Unlock()
			l.SetDelegate(hooks.lnDelegate)
			v.sock.SetSocketListenerForPort(l, port)
			v.mu.Lock()
			if v.listeners == nil {
				v.listeners = map[uint32]virtualization.VZVirtioSocketListener{}
			}
			old, replaced := v.listeners[port]
			v.listeners[port] = l
			v.mu.Unlock()
			if replaced {
				hooks.mu.Lock()
				delete(hooks.accepts, old.ID)
				hooks.mu.Unlock()
				release(old.ID)
			}
		})
	}) {
		return errReleased
	}
	return err
}

// Unlisten removes the listener of a port. The framework refuses every new
// connection of the guest to it. Connections that Listen already handed out
// stay open. Dial is not affected: it opens connections to the guest.
// Unlisten must not be called from accept, which runs on the queue of the VM.
func (v *VM) Unlisten(port uint32) {
	_ = v.onQueue(func() {
		if v.released {
			return
		}
		pooled(func() {
			v.mu.Lock()
			l, ok := v.listeners[port]
			delete(v.listeners, port)
			v.mu.Unlock()
			if ok {
				v.sock.RemoveSocketListenerForPort(port)
				hooks.mu.Lock()
				delete(hooks.accepts, l.ID)
				hooks.mu.Unlock()
				release(l.ID)
			}
		})
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
	if !v.async(func() {
		if v.released {
			res <- result{err: errReleased}
			return
		}
		block, cleanup := virtualization.NewVirtioSocketConnectionErrorBlock(func(c *virtualization.VZVirtioSocketConnection, err error) {
			if err != nil {
				res <- result{err: fmt.Errorf("vmm: connect to port %d: %w", port, err)}
				return
			}
			nc, cerr := connOf(*c)
			res <- result{nc, cerr}
		})
		defer cleanup()
		objc.Send[objc.ID](v.sock.ID, objc.Sel("connectToPort:completionHandler:"), port, block)
	}) {
		res <- result{err: errReleased}
	}
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
