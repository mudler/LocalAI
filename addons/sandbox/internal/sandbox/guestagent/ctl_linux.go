package guestagent

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
)

// Environment of the agent in a gVisor sandbox. EnvCtl names the control
// folder that the host bind-mounts into the sandbox, and its presence turns
// the Unix socket mode on. EnvPorts lists the guest ports to forward, as
// "3128,7000"; the forwarder of port N dials port-N.sock in the control
// folder.
const (
	EnvCtl   = "SANDBOX_AGENT_CTL"
	EnvPorts = "SANDBOX_AGENT_PORTS"
)

// CtlDir is the control folder of a sandbox: the agent socket and the port
// sockets live in it. With --host-uds=all a guest process can connect to any
// socket it can reach by path, so the agent hides the folder before it
// listens (Hide) and keeps a descriptor for its own use. Every name goes
// through that descriptor, so it works before and after Hide.
type CtlDir struct {
	fd   int
	path string
}

// OpenCtlDir opens the control folder at path.
func OpenCtlDir(path string) (*CtlDir, error) {
	const oPath = 0x200000 // O_PATH
	fd, err := syscall.Open(path, oPath|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return &CtlDir{fd: fd, path: path}, nil
}

// Path returns the path of name in the folder through the descriptor.
func (c *CtlDir) Path(name string) string {
	return fmt.Sprintf("/proc/self/fd/%d/%s", c.fd, name)
}

// Listen listens on the Unix socket name in the folder. It removes a stale
// socket file first.
func (c *CtlDir) Listen(name string) (net.Listener, error) {
	p := c.Path(name)
	_ = os.Remove(p)
	return net.Listen("unix", p)
}

// Dial connects to the Unix socket name in the folder.
func (c *CtlDir) Dial(name string) (net.Conn, error) {
	return net.Dial("unix", c.Path(name))
}

// Hide makes the folder unreachable by path for every other process: it
// stops this process from being dumpable, so the /proc/<pid>/fd entries of
// the agent are closed to its children, and mounts an empty tmpfs over the
// folder. The agent keeps working through its descriptor.
//
// Listen and Dial through /proc/self/fd rely on the agent keeping
// CAP_DAC_OVERRIDE in its effective set: on Linux the /proc/self/fd entries
// of a non-dumpable process belong to root. Dropping that capability from the
// agent breaks them.
func (c *CtlDir) Hide() error {
	if err := SetNotDumpable(); err != nil {
		return err
	}
	if err := syscall.Mount("tmpfs", c.path, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "mode=0755"); err != nil {
		return &os.PathError{Op: "mount tmpfs", Path: c.path, Err: err}
	}
	return nil
}

// SetNotDumpable sets PR_SET_DUMPABLE to 0 for this process.
func SetNotDumpable() error {
	const prSetDumpable = 4
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); e != 0 {
		return e
	}
	return nil
}

// ClearAmbient clears the ambient capability set of every thread of this
// process, so a command that the agent starts gets only the capabilities the
// Spawner raises for it. The process needs a binary without cgo.
func ClearAmbient() error {
	const prCapAmbient, prCapAmbientClearAll = 47, 4
	if _, _, e := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientClearAll, 0, 0, 0, 0); e != 0 {
		return e
	}
	return nil
}

// SwitchListener serves a Server on a listener that can be closed and opened
// again. gVisor cannot save a sandbox with a bound host socket, so the host
// asks the agent to close the listener before a checkpoint, and SIGUSR1 opens
// it again after the restore.
type SwitchListener struct {
	// Listen opens the listener.
	Listen func() (net.Listener, error)

	mu  sync.Mutex
	cur net.Listener
}

// Start opens the listener and serves srv on it in the background. It does
// nothing when a listener is open.
func (s *SwitchListener) Start(srv *Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil {
		return nil
	}
	l, err := s.Listen()
	if err != nil {
		return err
	}
	s.cur = l
	go func() {
		defer logPanic("serve")
		_ = srv.Serve(RetryListener{Listener: l})
	}()
	return nil
}

// Stop closes the listener. Connections that are open stay open.
func (s *SwitchListener) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return nil
	}
	err := s.cur.Close()
	s.cur = nil
	return err
}
