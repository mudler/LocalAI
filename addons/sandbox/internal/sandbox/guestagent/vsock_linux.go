package guestagent

import (
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// The standard library has no vsock address type, so the calls are raw.
const (
	afVsock = 40         // AF_VSOCK
	cidAny  = 0xFFFFFFFF // VMADDR_CID_ANY
)

type sockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Flags     uint8
	Zero      [3]uint8
}

// VsockAddr is a vsock address.
type VsockAddr struct{ CID, Port uint32 }

func (a VsockAddr) Network() string { return "vsock" }
func (a VsockAddr) String() string {
	return strconv.FormatUint(uint64(a.CID), 10) + ":" + strconv.FormatUint(uint64(a.Port), 10)
}

func vsockSocket(flags int) (int, error) {
	return syscall.Socket(afVsock, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC|flags, 0)
}

// ListenVsock listens on a vsock port of this guest. It serves the host only
// (peerAllowed).
func ListenVsock(port uint32) (net.Listener, error) {
	fd, err := vsockSocket(syscall.SOCK_NONBLOCK)
	if err != nil {
		return nil, err
	}
	sa := sockaddrVM{Family: afVsock, Port: port, CID: cidAny}
	if _, _, e := syscall.RawSyscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&sa)), unsafe.Sizeof(sa)); e != 0 {
		_ = syscall.Close(fd)
		return nil, e
	}
	if err := syscall.Listen(fd, 128); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return &vsockListener{f: os.NewFile(uintptr(fd), "vsock-listener"), port: port}, nil
}

type vsockListener struct {
	f      *os.File
	port   uint32
	allow  func(sockaddrVM) bool // nil means a vsock peer that peerAllowed accepts
	closed atomic.Bool           // set by Close; Accept then reports net.ErrClosed
}

// peerAllowed reports whether a connection from the vsock peer CID may be
// served: only the host. A process in the guest can connect to its own
// listener through the local CIDs (VMADDR_CID_LOCAL, or the guest's own CID),
// and must not reach the agent that way.
func peerAllowed(cid uint32) bool { return cid == HostCID }

func (l *vsockListener) allowed(peer sockaddrVM) bool {
	if l.allow != nil {
		return l.allow(peer)
	}
	return peer.Family == afVsock && peerAllowed(peer.CID)
}

// Accept returns the next connection from the host. A connection from any
// other peer is closed at once and never returned. After Close it returns
// net.ErrClosed, the error RetryListener and Forward stop on.
func (l *vsockListener) Accept() (net.Conn, error) {
	for {
		if l.closed.Load() {
			return nil, net.ErrClosed
		}
		rc, err := l.f.SyscallConn()
		if err != nil {
			return nil, l.closedErr(err)
		}
		var nfd int
		var aerr error
		var peer sockaddrVM
		err = rc.Read(func(fd uintptr) bool {
			n := uint32(unsafe.Sizeof(peer))
			r, _, e := syscall.Syscall6(syscall.SYS_ACCEPT4, fd, uintptr(unsafe.Pointer(&peer)), uintptr(unsafe.Pointer(&n)), syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK, 0, 0)
			if e == syscall.EAGAIN {
				return false
			}
			if e != 0 {
				aerr = e
			}
			nfd = int(r)
			return true
		})
		if err != nil {
			return nil, l.closedErr(err)
		}
		if aerr != nil {
			return nil, l.closedErr(aerr)
		}
		if !l.allowed(peer) {
			_ = syscall.Close(nfd)
			continue
		}
		return newVsockConn(nfd, VsockAddr{CID: peer.CID, Port: l.port}), nil
	}
}

// closedErr maps the error of a closed file to net.ErrClosed. The runtime
// poller reports a closed file with its own error, which a caller that
// stops on net.ErrClosed would not recognise.
func (l *vsockListener) closedErr(err error) error {
	if l.closed.Load() {
		return net.ErrClosed
	}
	return err
}

// Close closes the listener. A blocked Accept returns net.ErrClosed.
func (l *vsockListener) Close() error {
	l.closed.Store(true)
	return l.f.Close()
}
func (l *vsockListener) Addr() net.Addr { return VsockAddr{CID: cidAny, Port: l.port} }

// DialVsock connects to a vsock port; HostCID is the host.
func DialVsock(cid, port uint32) (net.Conn, error) {
	fd, err := vsockSocket(0)
	if err != nil {
		return nil, err
	}
	sa := sockaddrVM{Family: afVsock, Port: port, CID: cid}
	for {
		_, _, e := syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd), uintptr(unsafe.Pointer(&sa)), unsafe.Sizeof(sa))
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			_ = syscall.Close(fd)
			return nil, e
		}
		break
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return newVsockConn(fd, VsockAddr{CID: cid, Port: port}), nil
}

// vsockConn is a nonblocking vsock socket. os.File gives it the runtime
// poller, deadlines and a Close that interrupts Read.
type vsockConn struct {
	*os.File
	remote VsockAddr
}

func newVsockConn(fd int, remote VsockAddr) *vsockConn {
	return &vsockConn{File: os.NewFile(uintptr(fd), "vsock"), remote: remote}
}

func (c *vsockConn) LocalAddr() net.Addr  { return VsockAddr{CID: cidAny} }
func (c *vsockConn) RemoteAddr() net.Addr { return c.remote }

// CloseWrite shuts down the sending side.
func (c *vsockConn) CloseWrite() error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.Shutdown(int(fd), syscall.SHUT_WR) }); err != nil {
		return err
	}
	return serr
}
