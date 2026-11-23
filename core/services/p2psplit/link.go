package p2psplit

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// dialTimeout bounds one dial of a data stream.
const dialTimeout = 30 * time.Second

// loopback is the address the forwarders listen on.
const loopback = "127.0.0.1"

// localForwarder accepts TCP connections on a loopback port and hands each
// one to dial. It is the MLX counterpart of p2pnet.Forward: each stream
// starts with a ring header, which p2pnet.Forward cannot write.
type localForwarder struct {
	ln   net.Listener
	dial func(ctx context.Context) (net.Conn, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	active map[net.Conn]struct{}
}

// newLocalForwarder listens on 127.0.0.1:port (0 picks a port).
func newLocalForwarder(ctx context.Context, port int, dial func(ctx context.Context) (net.Conn, error)) (*localForwarder, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(loopback, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	f := &localForwarder{ln: ln, dial: dial, active: map[net.Conn]struct{}{}}
	f.ctx, f.cancel = context.WithCancel(ctx)
	f.wg.Go(f.accept)
	f.wg.Go(func() {
		<-f.ctx.Done()
		_ = f.ln.Close()
	})
	return f, nil
}

// addr is the loopback address of the forwarder, as "127.0.0.1:port".
func (f *localForwarder) addr() string { return f.ln.Addr().String() }

func (f *localForwarder) port() int {
	if a, ok := f.ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

func (f *localForwarder) close() {
	f.cancel()
	f.mu.Lock()
	for c := range f.active {
		_ = c.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

func (f *localForwarder) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			if f.ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				xlog.Warn("p2p split: forwarder stopped accepting", "error", err)
			}
			return
		}
		if !f.track(c, true) {
			_ = c.Close()
			return
		}
		f.wg.Go(func() {
			defer f.track(c, false)
			ctx, cancel := context.WithTimeout(f.ctx, dialTimeout)
			remote, err := f.dial(ctx)
			cancel()
			if err != nil {
				xlog.Debug("p2p split: ring dial failed", "error", err)
				_ = c.Close()
				return
			}
			if !f.track(remote, true) {
				_ = c.Close()
				_ = remote.Close()
				return
			}
			defer f.track(remote, false)
			if err := p2pnet.Pipe(c, remote); err != nil {
				xlog.Debug("p2p split: ring connection ended", "error", err)
			}
		})
	}
}

// track adds or removes c; it refuses to add after close.
func (f *localForwarder) track(c net.Conn, add bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !add {
		delete(f.active, c)
		return true
	}
	if f.ctx.Err() != nil {
		return false
	}
	f.active[c] = struct{}{}
	return true
}

// dialRing opens a ProtoMLX stream to p and writes the header for rank.
func dialRing(ctx context.Context, h *p2pnet.Host, p p2pnet.ID, rank int, opts ...p2pnet.DialOption) (net.Conn, error) {
	c, err := h.Dial(ctx, p, ProtoMLX, opts...)
	if err != nil {
		return nil, err
	}
	if err := writeMsg(c, ringHeader{Rank: rank}); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// pipeTCP connects c to the TCP address addr and copies both ways until one
// side ends.
func pipeTCP(ctx context.Context, c net.Conn, addr string) {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	local, err := d.DialContext(dctx, "tcp", addr)
	cancel()
	if err != nil {
		xlog.Debug("p2p split: local dial failed", "addr", addr, "error", err)
		_ = c.Close()
		return
	}
	if err := p2pnet.Pipe(c, local); err != nil {
		xlog.Debug("p2p split: connection ended", "addr", addr, "error", err)
	}
}

// serveListener accepts on l until it closes and runs fn on each connection.
func serveListener(wg *sync.WaitGroup, l net.Listener, fn func(net.Conn)) {
	wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Go(func() { fn(c) })
		}
	})
}
