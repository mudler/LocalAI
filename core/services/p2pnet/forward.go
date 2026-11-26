package p2pnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/mudler/xlog"
)

const (
	forwardDialAttempts = 3
	forwardDialTimeout  = 30 * time.Second
	forwardRetryDelay   = 250 * time.Millisecond
)

// ForwardState is the state of a Forwarder.
type ForwardState string

const (
	// ForwardListening means that no connection was forwarded yet.
	ForwardListening ForwardState = "listening"
	// ForwardUp means that the last dial to the peer worked.
	ForwardUp ForwardState = "up"
	// ForwardDown means that the last dial to the peer failed.
	ForwardDown ForwardState = "down"
	// ForwardClosed means that the forwarder stopped.
	ForwardClosed ForwardState = "closed"
)

// ForwardStatus is reported to the WithStatus callback when the state of a
// Forwarder changes.
type ForwardStatus struct {
	State ForwardState
	// Err is the dial error of a ForwardDown state.
	Err error
	// Path is the path of the last connection that was forwarded.
	Path Path
}

// ForwardOption configures Forward.
type ForwardOption func(*forwardOptions)

type forwardOptions struct {
	dial   []DialOption
	status func(ForwardStatus)
}

// WithDialOptions sets the options of every dial of the forwarder.
func WithDialOptions(opts ...DialOption) ForwardOption {
	return func(o *forwardOptions) { o.dial = append(o.dial, opts...) }
}

// WithStatus sets a callback for state changes. Calls are serialised; the
// callback must return quickly.
func WithStatus(fn func(ForwardStatus)) ForwardOption {
	return func(o *forwardOptions) { o.status = fn }
}

// Forwarder accepts TCP connections on a loopback address and forwards each
// one through a new stream to a peer.
type Forwarder struct {
	n     *Host
	peer  peer.ID
	proto protocol.ID
	opts  forwardOptions
	ln    net.Listener

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	state  ForwardState
	active map[net.Conn]struct{}
}

// Forward listens on listen, a loopback address with the port the caller
// wants to keep (for example the port a browser origin depends on), and
// forwards each connection to p on proto. Each connection dials the peer
// again, with a few retries, so the forwarder outlives a dropped path. A
// busy port is an error. The forwarder stops when ctx ends or on Close.
func (n *Host) Forward(ctx context.Context, p peer.ID, proto protocol.ID, listen string, opts ...ForwardOption) (*Forwarder, error) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("p2pnet: forward address: %w", err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("p2pnet: forward address %q is not a loopback address", listen)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("p2pnet: forward listen: %w", err)
	}
	f := &Forwarder{n: n, peer: p, proto: proto, ln: ln, state: ForwardListening, active: map[net.Conn]struct{}{}}
	for _, o := range opts {
		o(&f.opts)
	}
	f.ctx, f.cancel = context.WithCancel(ctx)
	f.wg.Go(f.accept)
	f.wg.Go(func() {
		<-f.ctx.Done()
		_ = f.ln.Close()
	})
	return f, nil
}

// Addr returns the local address of the forwarder.
func (f *Forwarder) Addr() net.Addr { return f.ln.Addr() }

// Close stops the forwarder and ends the connections it forwards.
func (f *Forwarder) Close() error {
	f.cancel()
	f.mu.Lock()
	for c := range f.active {
		resetConn(c)
	}
	f.mu.Unlock()
	f.wg.Wait()
	f.report(ForwardStatus{State: ForwardClosed})
	return nil
}

func (f *Forwarder) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			if f.ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				xlog.Warn("p2p: forwarder stopped accepting", "error", err)
			}
			return
		}
		if !f.track(c, true) {
			_ = c.Close()
			return
		}
		f.wg.Go(func() {
			defer f.track(c, false)
			f.serve(c)
		})
	}
}

func (f *Forwarder) serve(local net.Conn) {
	remote, err := f.dial()
	if err != nil {
		f.report(ForwardStatus{State: ForwardDown, Err: err})
		resetConn(local)
		return
	}
	if !f.track(remote, true) {
		resetConn(local)
		resetConn(remote)
		return
	}
	defer f.track(remote, false)
	if pc, ok := remote.(*Conn); ok {
		f.report(ForwardStatus{State: ForwardUp, Path: f.n.pathOf(pc.Stream.Conn())})
	}
	if err := Pipe(local, remote); err != nil {
		xlog.Debug("p2p: forwarded connection failed", "peer", f.peer.String(), "error", err)
	}
}

func (f *Forwarder) dial() (net.Conn, error) {
	timeout := forwardDialTimeout
	if o := newDialOptions(f.opts.dial); len(o.hints) > 0 {
		// The wait for a hole punch comes on top of the dial itself.
		timeout += 2 * o.punchWait
	}
	var err error
	for attempt := range forwardDialAttempts {
		if attempt > 0 {
			select {
			case <-f.ctx.Done():
				return nil, f.ctx.Err()
			case <-time.After(forwardRetryDelay << (attempt - 1)):
			}
		}
		ctx, cancel := context.WithTimeout(f.ctx, timeout)
		var c net.Conn
		c, err = f.n.Dial(ctx, f.peer, f.proto, f.opts.dial...)
		cancel()
		if err == nil {
			return c, nil
		}
		// A limited path is the caller's decision, and a peer reachable only
		// through a hint relay needs a relay: neither is a transient failure.
		if errors.Is(err, ErrPathLimited) || errors.Is(err, ErrNoDirectPath) || f.ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, err
}

// track adds or removes c from the active set. It refuses to add after
// Close, so that no connection outlives the forwarder.
func (f *Forwarder) track(c net.Conn, add bool) bool {
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

func (f *Forwarder) report(st ForwardStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == st.State && st.Err == nil || f.state == ForwardClosed {
		return
	}
	f.state = st.State
	if f.opts.status != nil {
		f.opts.status(st)
	}
}
