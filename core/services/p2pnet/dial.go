package p2pnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
)

// limitedConnReason is passed to libp2p to allow streams over limited
// (relayed) connections; Dial checks the limits itself.
const limitedConnReason = "localai"

// Need is what a caller needs from a connection. A relay may limit the data
// and the time of a relayed connection; Dial compares those limits with the
// Need and refuses a path that is too small. The zero Need accepts any path:
// use it for control traffic, and when the user chose "use anyway".
type Need struct {
	// Bytes is the data the caller expects to move, in both directions.
	// A negative value means "no data limit at all".
	Bytes int64
	// Duration is how long the caller expects to keep the stream open.
	// A negative value means "no time limit at all".
	Duration time.Duration
}

// NeedBulk accepts a direct path or a relayed path without a data limit.
var NeedBulk = Need{Bytes: -1}

// SatisfiedBy reports whether path can carry n.
func (n Need) SatisfiedBy(path Path) bool {
	if !path.Limited || n == (Need{}) {
		return true
	}
	if n.Bytes < 0 && path.LimitBytes > 0 || n.Duration < 0 && path.LimitDuration > 0 {
		return false
	}
	if path.LimitBytes == 0 && path.LimitDuration == 0 {
		// Limited, but the relay did not say how: only the zero Need fits.
		return false
	}
	if n.Bytes > 0 && path.LimitBytes > 0 && path.LimitBytes < n.Bytes {
		return false
	}
	if n.Duration > 0 && path.LimitDuration > 0 && path.LimitDuration < n.Duration {
		return false
	}
	return true
}

// ErrPathLimited is matched by errors.Is for a PathLimitedError.
var ErrPathLimited = errors.New("p2pnet: the path limits are below the need")

// PathLimitedError is returned by Dial when the only path to the peer is a
// relayed connection whose limits are below the Need. The caller may dial
// again with a smaller Need, or with the zero Need to use the path anyway.
type PathLimitedError struct {
	Path Path
	Need Need
}

func (e *PathLimitedError) Error() string {
	return fmt.Sprintf("p2pnet: relayed path through %s is limited to %d bytes and %s, below the need of %d bytes and %s",
		e.Path.Relay, e.Path.LimitBytes, e.Path.LimitDuration, e.Need.Bytes, e.Need.Duration)
}

// Is matches ErrPathLimited.
func (e *PathLimitedError) Is(target error) bool { return target == ErrPathLimited }

// DialOption configures Dial.
type DialOption func(*dialOptions)

type dialOptions struct {
	addrs  []ma.Multiaddr
	relays []peer.AddrInfo
	need   Need
}

// WithAddrs adds known addresses of the peer, such as those of an invite
// link, so that Dial needs no lookup.
func WithAddrs(addrs ...ma.Multiaddr) DialOption {
	return func(o *dialOptions) { o.addrs = append(o.addrs, addrs...) }
}

// WithRelays adds relays through which the peer may be reached. Dial adds a
// circuit address through each relay; it does no lookup.
func WithRelays(relays ...peer.AddrInfo) DialOption {
	return func(o *dialOptions) { o.relays = append(o.relays, relays...) }
}

// WithNeed sets what the caller needs from the path. See Need.
func WithNeed(n Need) DialOption {
	return func(o *dialOptions) { o.need = n }
}

// Dial opens a stream to p on proto and returns it as a *Conn. Limited
// relayed connections are allowed; when the path's reported limits are
// below the Need, Dial resets the stream and returns a *PathLimitedError.
func (n *Host) Dial(ctx context.Context, p peer.ID, proto protocol.ID, opts ...DialOption) (net.Conn, error) {
	var o dialOptions
	for _, opt := range opts {
		opt(&o)
	}
	ps := n.h.Peerstore()
	if len(o.addrs) > 0 {
		ps.AddAddrs(p, o.addrs, peerstore.TempAddrTTL)
	}
	for _, r := range o.relays {
		if r.ID == p || r.ID == n.h.ID() {
			continue
		}
		ps.AddAddrs(r.ID, r.Addrs, peerstore.TempAddrTTL)
		suffix, err := ma.NewMultiaddr("/p2p/" + r.ID.String() + "/p2p-circuit")
		if err != nil {
			return nil, fmt.Errorf("p2pnet: relay address: %w", err)
		}
		for _, a := range r.Addrs {
			ps.AddAddr(p, a.Encapsulate(suffix), peerstore.TempAddrTTL)
		}
	}
	s, err := n.h.NewStream(network.WithAllowLimitedConn(ctx, limitedConnReason), p, proto)
	if err != nil {
		return nil, fmt.Errorf("p2pnet: dial %s on %s: %w", p, proto, err)
	}
	if path := n.pathOf(s.Conn()); !o.need.SatisfiedBy(path) {
		_ = s.Reset()
		return nil, &PathLimitedError{Path: path, Need: o.need}
	}
	return &Conn{Stream: s}, nil
}

// Listen accepts streams on proto as a net.Listener. Each stream must pass
// the host's Gate and then admit (when admit is not nil); a refused stream
// is reset before the listener sees it. One listener per protocol.
func (n *Host) Listen(proto protocol.ID, admit func(peer.ID) bool) (net.Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, net.ErrClosed
	}
	if _, ok := n.listeners[proto]; ok {
		return nil, fmt.Errorf("p2pnet: a listener for %s exists", proto)
	}
	l := &listener{n: n, proto: proto, admit: admit, ch: make(chan net.Conn), done: make(chan struct{})}
	n.listeners[proto] = l
	n.h.SetStreamHandler(proto, l.handle)
	return l, nil
}

type listener struct {
	n     *Host
	proto protocol.ID
	admit func(peer.ID) bool
	ch    chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *listener) handle(s network.Stream) {
	p := s.Conn().RemotePeer()
	if !l.n.gate.AdmitStream(p, l.proto) || (l.admit != nil && !l.admit(p)) {
		xlog.Debug("p2p: refused stream", "peer", p.String(), "protocol", string(l.proto))
		_ = s.Reset()
		return
	}
	select {
	case l.ch <- &Conn{Stream: s}:
	case <-l.done:
		_ = s.Reset()
	}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.n.h.RemoveStreamHandler(l.proto)
		l.n.mu.Lock()
		if l.n.listeners[l.proto] == l {
			delete(l.n.listeners, l.proto)
		}
		l.n.mu.Unlock()
	})
	return nil
}

func (l *listener) Addr() net.Addr { return Addr{Peer: l.n.h.ID()} }

// HTTPClient returns an HTTP client whose connections are streams to p on
// ProtoHTTP. The host part of the request URL is not used to dial; use the
// peer ID, as in "http://<peer ID>/readyz". Keep-alive reuses streams.
func (n *Host) HTTPClient(p peer.ID, opts ...DialOption) *http.Client {
	t := httpclient.HardenedTransport()
	t.Proxy = nil
	t.ForceAttemptHTTP2 = false
	t.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return n.Dial(ctx, p, ProtoHTTP, opts...)
	}
	return httpclient.New(httpclient.WithTransport(t))
}
