package chv

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

// portServer serves the host side of the guest ports: Cloud Hypervisor
// connects to vsock.sock_<port> for every connection the guest opens to
// host vsock port <port>.
type portServer struct {
	ls     []net.Listener
	cancel context.CancelFunc
}

// maxPortConns is how many connections to port handlers one sandbox may have
// at a time, over all its ports. The guest can open connections as fast as it
// likes, and each one costs the host a goroutine and a file descriptor.
const maxPortConns = 64

func servePorts(dir string, ports map[int]sandbox.PortHandler) (*portServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ps := &portServer{cancel: cancel}
	slots := make(chan struct{}, maxPortConns)
	for port, h := range ports {
		p := filepath.Join(dir, "vsock.sock_"+itoa(uint64(port))) // #nosec G115 -- a port number is not negative
		_ = os.Remove(p)
		l, err := net.Listen("unix", p)
		if err != nil {
			ps.close()
			return nil, err
		}
		ps.ls = append(ps.ls, l)
		go servePortListener(ctx, l, h, slots)
	}
	return ps, nil
}

// servePortListener hands each connection to h. A transient Accept error
// (EMFILE, a connection aborted in the queue) is retried with a backoff; the
// loop returns only when the listener is closed. slots limits the handlers
// that run at once: a connection that finds no free slot is closed.
func servePortListener(ctx context.Context, l net.Listener, h sandbox.PortHandler, slots chan struct{}) {
	for {
		c, err := guestagent.RetryListener{Listener: l}.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				h(ctx, c)
			}()
		default:
			_ = c.Close()
		}
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
