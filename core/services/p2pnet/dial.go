package p2pnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
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

// DefaultPunchWait is how long Dial waits, through a hint relay, for a hole
// punch to open a direct connection. The puncher makes three attempts with a
// dial timeout of 10 s each.
const DefaultPunchWait = 30 * time.Second

// ErrNoDirectPath is matched by errors.Is for a NoDirectPathError.
var ErrNoDirectPath = errors.New("p2pnet: no direct connection to the peer, and its hint relays carry no data")

// NoDirectPathError is returned by Dial when the peer was reached only
// through a hint relay and no hole punch opened a direct connection in time.
// A hint relay never carries application data, so the peer needs a relay
// that does (WithRelays). It is a final answer, not a transient failure.
type NoDirectPathError struct {
	// Peer is the peer that Dial tried to reach.
	Peer peer.ID
	// Relay is the hint relay through which the peer was reached.
	Relay peer.ID
	// Wait is how long Dial waited for the direct connection.
	Wait time.Duration
}

func (e *NoDirectPathError) Error() string {
	return fmt.Sprintf("p2pnet: no direct connection to %s after %s of hole punching through hint relay %s: a relay that carries data is needed",
		e.Peer, e.Wait, e.Relay)
}

// Is matches ErrNoDirectPath.
func (e *NoDirectPathError) Is(target error) bool { return target == ErrNoDirectPath }

// DialOption configures Dial.
type DialOption func(*dialOptions)

type dialOptions struct {
	addrs     []ma.Multiaddr
	relays    []peer.AddrInfo
	hints     []peer.AddrInfo
	need      Need
	punchWait time.Duration
}

func newDialOptions(opts []DialOption) dialOptions {
	o := dialOptions{punchWait: DefaultPunchWait}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithAddrs adds known addresses of the peer, such as those of an invite
// link, so that Dial needs no lookup.
func WithAddrs(addrs ...ma.Multiaddr) DialOption {
	return func(o *dialOptions) { o.addrs = append(o.addrs, addrs...) }
}

// WithRelays adds relays through which the peer may be reached. Dial adds a
// circuit address through each relay; it does no lookup. These relays may
// carry application data, also when they limit the connection.
func WithRelays(relays ...peer.AddrInfo) DialOption {
	return func(o *dialOptions) { o.relays = append(o.relays, relays...) }
}

// WithHints adds hint relays: public relays on which the peer holds a
// reservation. Dial uses only their public IPv4 and IPv6 addresses (see
// Config.PrivateHints), once for each relay. Dial uses them only after the direct addresses and the
// relays of WithRelays failed, or at once when it has neither. Through a
// hint, Dial opens no stream: it connects only so that the peers can run a
// hole punch, waits for the direct connection (see WithPunchWait), and then
// closes the connection through the hint. When no direct connection opens,
// Dial returns a *NoDirectPathError. The host never sends application data
// through a relay that a dial named as a hint, unless a dial to the same
// peer names it with WithRelays. Once a dial to the peer named
// hints, the same holds for every relay that no dial to that peer named with
// WithRelays, such as the other public relays that identify or a DHT lookup
// stored: the host dials them only during a hole punch and returns a
// *NoDirectPathError when the only path goes through one. A relay that a
// dial to another peer names as a carrier or a hint changes nothing for this
// peer.
func WithHints(relays ...peer.AddrInfo) DialOption {
	return func(o *dialOptions) { o.hints = append(o.hints, relays...) }
}

// WithPunchWait sets how long Dial waits for a direct connection through a
// hint relay. Zero or less means DefaultPunchWait.
func WithPunchWait(d time.Duration) DialOption {
	return func(o *dialOptions) {
		if d <= 0 {
			d = DefaultPunchWait
		}
		o.punchWait = d
	}
}

// WithNeed sets what the caller needs from the path. See Need.
func WithNeed(n Need) DialOption {
	return func(o *dialOptions) { o.need = n }
}

// Dial opens a stream to p on proto and returns it as a *Conn.
//
// Dial tries the paths in two phases. The first uses the known and direct
// addresses and the relays of WithRelays; limited relayed connections are
// allowed, and when the path's reported limits are below the Need, Dial
// resets the stream and returns a *PathLimitedError. The second phase uses
// the hint relays of WithHints (see there) and runs when the first one
// failed, or at once when the caller gave hints and no address or relay.
func (n *Host) Dial(ctx context.Context, p peer.ID, proto protocol.ID, opts ...DialOption) (net.Conn, error) {
	o := newDialOptions(opts)
	ps := n.h.Peerstore()
	if len(o.addrs) > 0 {
		ps.AddAddrs(p, o.addrs, peerstore.TempAddrTTL)
	}
	carriers, err := n.addCircuits(p, o.relays)
	if err != nil {
		return nil, err
	}
	var named []peer.AddrInfo
	for _, h := range o.hints {
		if h.ID == p || h.ID == n.h.ID() || slices.Contains(carriers, h.ID) {
			continue
		}
		named = append(named, h)
	}
	// A hint guards the peer even when none of its addresses is usable: the
	// link said that the peer's relays are public.
	n.hints.note(p, carriers, len(named) > 0)
	hints := usableHints(named, n.privateHints)

	var firstErr error
	if len(hints) == 0 || len(o.addrs) > 0 || len(carriers) > 0 {
		s, err := n.openStream(ctx, p, proto, o.punchWait)
		if err == nil {
			return n.checkNeed(s, o.need)
		}
		if len(hints) == 0 || ctx.Err() != nil {
			return nil, err
		}
		firstErr = err
	}
	s, err := n.dialThroughHints(ctx, p, proto, hints, o.punchWait)
	if err != nil {
		if firstErr != nil && !errors.Is(err, ErrNoDirectPath) {
			err = fmt.Errorf("%w; through the hint relays: %w", firstErr, err)
		}
		return nil, err
	}
	return n.checkNeed(s, o.need)
}

// usableHints returns hints with only the addresses that Dial may use, one
// entry for each relay. A hint comes from a link that anyone can write, so it
// must not make the host dial into its own networks: only IPv4 and IPv6
// addresses are kept (no DNS names, which could resolve to a private
// address, and no IPv6 zones), and only public ones unless private is set.
// A relay with no usable address is left out.
func usableHints(hints []peer.AddrInfo, private bool) []peer.AddrInfo {
	var out []peer.AddrInfo
	index := map[peer.ID]int{}
	for _, h := range hints {
		i, ok := index[h.ID]
		if !ok {
			i = len(out)
			out = append(out, peer.AddrInfo{ID: h.ID})
		}
		for _, a := range h.Addrs {
			if !usableHintAddr(a, private) || slices.ContainsFunc(out[i].Addrs, a.Equal) {
				continue
			}
			out[i].Addrs = append(out[i].Addrs, a)
		}
		if !ok && len(out[i].Addrs) == 0 {
			out = out[:i]
			continue
		}
		index[h.ID] = i
	}
	return out
}

func usableHintAddr(a ma.Multiaddr, private bool) bool {
	if len(a) == 0 {
		return false
	}
	switch a[0].Code() {
	case ma.P_IP4, ma.P_IP6:
	default:
		return false
	}
	return private || manet.IsPublicAddr(a)
}

// addCircuits adds a circuit address of p through each relay to the
// peerstore and returns the relay IDs.
func (n *Host) addCircuits(p peer.ID, relays []peer.AddrInfo) ([]peer.ID, error) {
	ps := n.h.Peerstore()
	var ids []peer.ID
	for _, r := range relays {
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
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// openStream opens a stream to p on any connection that does not go through
// a hint relay. Limited connections are allowed.
func (n *Host) openStream(ctx context.Context, p peer.ID, proto protocol.ID, punchWait time.Duration) (network.Stream, error) {
	sctx := network.WithAllowLimitedConn(ctx, limitedConnReason)
	if hints, other := n.hintConns(p); len(hints) > 0 && !other {
		// Another dial waits for a hole punch on a connection through a
		// hint. With limited connections allowed, libp2p would take that
		// connection instead of dialling, so wait for the direct one too.
		sctx = network.WithDialPeerTimeout(ctx, punchWait)
	}
	s, err := n.h.NewStream(sctx, p, proto)
	if err != nil {
		return nil, fmt.Errorf("p2pnet: dial %s on %s: %w", p, proto, err)
	}
	if relay := n.hintRelayOf(s.Conn()); relay != "" {
		// A connection through a hint opened between the check above and
		// the stream. The stream carries no data yet: drop it.
		_ = s.Reset()
		return nil, &NoDirectPathError{Peer: p, Relay: relay}
	}
	return s, nil
}

// dialThroughHints connects to p through the hint relays and waits for a
// hole punch to open a direct connection. It opens the stream only on that
// direct connection.
func (n *Host) dialThroughHints(ctx context.Context, p peer.ID, proto protocol.ID, hints []peer.AddrInfo, punchWait time.Duration) (network.Stream, error) {
	done := n.hints.begin(p)
	defer done()
	if _, err := n.addCircuits(p, hints); err != nil {
		return nil, err
	}
	// Without WithAllowLimitedConn, libp2p connects through the hint, waits
	// for identify (which starts the hole punch on the peer's side), and
	// then waits up to the dial peer timeout for a direct connection.
	wctx, cancel := context.WithTimeout(ctx, 2*punchWait)
	defer cancel()
	s, err := n.h.NewStream(network.WithDialPeerTimeout(wctx, punchWait), p, proto)
	if err == nil && n.hintRelayOf(s.Conn()) != "" {
		_ = s.Reset()
		s, err = nil, network.ErrLimitedConn
	}
	if err == nil {
		done()
		n.closeHintConns(p)
		return s, nil
	}
	// Close the connections through the hints, unless another dial still
	// waits on them.
	hintConns, _ := n.hintConns(p)
	var relay peer.ID
	if len(hintConns) > 0 {
		relay = n.hintRelayOf(hintConns[0])
	}
	if done() == 0 {
		n.closeHintConns(p)
	}
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, ctx.Err()
	}
	if relay == "" {
		// The host never reached the peer through a hint, so the hole punch
		// never had a chance: report the dial error.
		return nil, fmt.Errorf("p2pnet: dial %s through hint relays: %w", p, err)
	}
	return nil, &NoDirectPathError{Peer: p, Relay: relay, Wait: punchWait}
}

// checkNeed returns s as a *Conn when its path can carry need.
func (n *Host) checkNeed(s network.Stream, need Need) (net.Conn, error) {
	if path := n.pathOf(s.Conn()); !need.SatisfiedBy(path) {
		_ = s.Reset()
		return nil, &PathLimitedError{Path: path, Need: need}
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
