package vz

import (
	"context"
	"net"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// portServer serves the host side of the guest ports of one VM: the machine
// calls accept for every connection the guest opens to host vsock port
// <port>. It lives as long as the VM.
type portServer struct {
	cancel context.CancelFunc
}

// maxPortConns is how many connections to port handlers one sandbox may have
// at a time, over all its ports. The guest can open connections as fast as it
// likes, and each one costs the host a goroutine and a file descriptor.
const maxPortConns = 64

// servePorts registers a listener on m for each port. It must run before the
// machine starts.
func servePorts(m machine, ports map[int]sandbox.PortHandler) (*portServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ps := &portServer{cancel: cancel}
	slots := make(chan struct{}, maxPortConns)
	for port, h := range ports {
		err := m.Listen(uint32(port), func(c net.Conn) { // #nosec G115 -- CheckPorts keeps a port between 1 and 65535
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					h(ctx, c)
				}()
			default:
				_ = c.Close()
			}
		})
		if err != nil {
			cancel()
			return nil, err
		}
	}
	return ps, nil
}

// close cancels the context of the handlers. Their connections end with the
// machine.
func (ps *portServer) close() {
	if ps == nil {
		return
	}
	ps.cancel()
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
