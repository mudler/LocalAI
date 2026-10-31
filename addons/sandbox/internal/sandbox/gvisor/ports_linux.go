//go:build linux

package gvisor

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

// maxPortConns is how many connections to port handlers one sandbox may have
// at a time, over all its ports. The guest can open connections as fast as it
// likes, and each one costs the host a goroutine and a file descriptor.
const maxPortConns = 64

// portServer serves the host side of the guest ports: the guest forwarder of
// port N connects to ctl/port-N.sock for every connection a command opens to
// 127.0.0.1:N.
type portServer struct {
	ls     []net.Listener
	cancel context.CancelFunc
	conns  connSet
	gated  atomic.Bool // while set, new connections are closed at once
}

func portSock(dir string, port int) string {
	return filepath.Join(dir, "ctl", "port-"+strconv.Itoa(port)+".sock")
}

func servePorts(dir string, ports map[int]sandbox.PortHandler) (*portServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ps := &portServer{cancel: cancel}
	slots := make(chan struct{}, maxPortConns)
	for port, h := range ports {
		p := portSock(dir, port)
		_ = os.Remove(p)
		l, err := net.Listen("unix", p)
		if err != nil {
			ps.close()
			return nil, err
		}
		ps.ls = append(ps.ls, l)
		go ps.serve(ctx, l, h, slots)
	}
	return ps, nil
}

// serve hands each connection to h. A transient Accept error is retried with
// a backoff; the loop returns only when the listener is closed. A connection
// that finds no free slot is closed.
func (ps *portServer) serve(ctx context.Context, l net.Listener, h sandbox.PortHandler, slots chan struct{}) {
	for {
		c, err := guestagent.RetryListener{Listener: l}.Accept()
		if err != nil {
			return
		}
		tc := ps.conns.add(c)
		if tc == nil {
			continue // the server is closed; add closed c
		}
		if ps.gated.Load() { // checked after add: closeConns after gate sees every earlier one
			_ = tc.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				h(ctx, tc)
			}()
		default:
			_ = tc.Close()
		}
	}
}

// gate makes the server close every new connection at once, and ungate lets
// them through again. Suspend gates before it closes the open connections, so
// that a guest process cannot open a new one before "runsc checkpoint".
func (ps *portServer) gate() {
	if ps != nil {
		ps.gated.Store(true)
	}
}

func (ps *portServer) ungate() {
	if ps != nil {
		ps.gated.Store(false)
	}
}

// closeConns closes the connections that handlers hold. A checkpoint of the
// sandbox fails while the host has a connection to a socket in the sandbox.
func (ps *portServer) closeConns() {
	if ps != nil {
		ps.conns.closeAll()
	}
}

// close stops the listeners and cancels the handlers' context.
func (ps *portServer) close() {
	if ps == nil {
		return
	}
	ps.cancel()
	for _, l := range ps.ls {
		_ = l.Close()
	}
	ps.ls = nil
	ps.conns.shutdown()
}

// connSet tracks open connections, so they can be closed together.
type connSet struct {
	mu     sync.Mutex
	m      map[*trackedConn]struct{}
	closed bool // set by shutdown: add refuses new connections
}

type trackedConn struct {
	net.Conn
	set *connSet
}

func (c *trackedConn) Close() error {
	c.set.mu.Lock()
	delete(c.set.m, c)
	c.set.mu.Unlock()
	return c.Conn.Close()
}

// add tracks c. After shutdown it closes c and returns nil.
func (s *connSet) add(c net.Conn) *trackedConn {
	tc := &trackedConn{Conn: c, set: s}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = c.Close()
		return nil
	}
	if s.m == nil {
		s.m = map[*trackedConn]struct{}{}
	}
	s.m[tc] = struct{}{}
	s.mu.Unlock()
	return tc
}

// shutdown closes all connections and makes add refuse new ones.
func (s *connSet) shutdown() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.closeAll()
}

func (s *connSet) closeAll() {
	s.mu.Lock()
	all := make([]*trackedConn, 0, len(s.m))
	for c := range s.m {
		all = append(all, c)
	}
	s.mu.Unlock()
	for _, c := range all {
		_ = c.Close()
	}
}

func portList(ports map[int]sandbox.PortHandler) []int {
	out := make([]int, 0, len(ports))
	for p := range ports {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func samePorts(a []int, b map[int]sandbox.PortHandler) bool {
	return slices.Equal(a, portList(b))
}
