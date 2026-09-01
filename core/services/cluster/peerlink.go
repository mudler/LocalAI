package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
	"github.com/mudler/xlog"
)

// PeerPath is the route a replica dials to open a peer link, and the route the
// HTTP layer registers the handler on. It lives here, with the dialler and the
// WebSocket adapter, so that core/services/cluster stays a leaf: the HTTP
// endpoints package imports this one, never the other way round.
//
// The literal is spelled out rather than derived from auth.ClusterPathPrefix
// because importing core/http/auth is exactly the dependency this package must
// not have. The two are kept from drifting apart by a spec in the endpoints
// package, which can see both.
const PeerPath = "/api/cluster/peer"

// ErrPeerUnreachable reports that a peer this deployment knows about could not
// be reached: the dial failed, the peer refused the credentials, or its
// multiplexer would not carry a stream.
//
// It is deliberately NOT a form of ErrInstanceNotFound, and the two must stay
// unmixable. A caller that sees absence is entitled to conclude a node is gone
// and reclaim what it was running; a caller that sees unreachability may only
// retry. Collapsing the two means a network hiccup between two healthy
// replicas evicts healthy workers. See unreachableError for how that is
// enforced rather than merely documented.
var ErrPeerUnreachable = errors.New("cluster: peer unreachable")

// unreachableError reports a peer that could not be reached, keeping the
// underlying cause in its message and out of its unwrap chain.
//
// Withholding the cause from errors.Is is the point. The dial path resolves a
// peer's address through the registry, so ErrInstanceNotFound is a cause this
// error can genuinely be built over: a row deleted between two attempts, for
// one. If the cause were unwrapped, that failure would satisfy both sentinels
// at once and every caller's absence check would fire on a transport problem.
// The guarantee therefore belongs to the type: no call site can leak absence
// through it, because there is no path by which absence gets out.
type unreachableError struct {
	peerID string
	cause  error
}

func (e *unreachableError) Error() string {
	return fmt.Sprintf("cluster: peer %q unreachable: %v", e.peerID, e.cause)
}

// Unwrap reports only ErrPeerUnreachable. The cause reaches a human through
// Error() and reaches no error-matching caller at all.
func (e *unreachableError) Unwrap() error { return ErrPeerUnreachable }

func unreachablePeer(peerID string, cause error) error {
	return &unreachableError{peerID: peerID, cause: cause}
}

// ErrPoolClosed reports an Open on a pool that has been shut down. It is a
// third condition on purpose: the pool being closed is a fact about this
// process and says nothing about whether the peer exists or answers.
var ErrPoolClosed = errors.New("cluster: peer pool is closed")

const (
	// peerLinkHandshakeTimeout bounds the WebSocket upgrade. It also bounds
	// how long Close can wait behind an in-flight dial, since a dial holds the
	// per-peer lock Close needs to reach the cached session.
	peerLinkHandshakeTimeout = 10 * time.Second

	// peerLinkInitialWindow is the per-stream receive window every stream on a
	// peer link starts at, raised from yamux's 256 KiB default.
	//
	// yamux already bounds head-of-line blocking with MaxMessageSize (64 KiB
	// by default), so one stream cannot monopolise the connection whatever the
	// window is. What the small default costs is the ramp: a stream carrying a
	// multi-megabyte gRPC message spends its first megabytes window-parked,
	// paying a round trip per doubling (stream.go:229) before it reaches full
	// rate. On a link that is also carrying token streams for other workers,
	// that ramp is pure added latency on the bulk transfer for no benefit.
	peerLinkInitialWindow = 4 * 1024 * 1024

	// peerLinkMaxWindow is the ceiling the auto-tuner may grow a stream to,
	// raised from yamux's 16 MiB default to cover the bandwidth-delay product
	// of a fast cross-zone link (roughly 31 MiB at 10 Gbps and 25 ms).
	//
	// The window is a cap on data received but not yet read, so the worst case
	// a peer can make this replica buffer is MaxIncomingStreams times this.
	// At yamux's default MaxIncomingStreams of 1000 that ceiling goes from
	// about 15.6 GiB to about 31 GiB per peer session, which is the figure to
	// size a replica against; it is why MaxIncomingStreams is left at the
	// default rather than raised alongside the window. Both are ceilings on
	// unread data and not allocations: yamux grows a stream's receive buffer
	// as data arrives.
	peerLinkMaxWindow = 32 * 1024 * 1024
)

// peerLinkConfig returns the yamux configuration for a replica-to-replica link.
func peerLinkConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.InitialStreamWindowSize = peerLinkInitialWindow
	cfg.MaxStreamWindowSize = peerLinkMaxWindow
	return cfg
}

// PeerPool dials peer replicas and keeps one multiplexed session per peer.
//
// A peer link carries traffic for every worker that peer owns, so it is pooled
// rather than dialled per request: a dial per relayed request would add a
// WebSocket handshake to every inference.
//
// The pool needs no knowledge of yamux error shapes to keep its cache honest.
// The two conditions worth reacting to arrive as OpenStream failures and are
// handled by the same retry: a peer that shut down gracefully hands its
// session ErrRemoteGoAway and closes it, and a session whose transport died
// hands out its shutdown error. Conditions scoped to a single stream, such as
// a peer resetting one request, never reach the pool at all, which is right:
// dropping the session over one reset request would tear down every other
// worker's traffic on that link.
type PeerPool struct {
	selfID string
	token  string
	reg    *Registry

	dialer *websocket.Dialer

	mu     sync.Mutex
	links  map[string]*peerLink
	closed bool
}

// peerLink is the cached session for one peer, plus the lock that serialises
// dialling it. The lock is per-peer so a slow or hanging dial to one peer does
// not hold up opens to any other.
type peerLink struct {
	mu   sync.Mutex
	sess *yamux.Session
}

// NewPeerPool returns a pool that dials peers as selfID, authenticating with
// the deployment's cluster token.
func NewPeerPool(selfID, token string, reg *Registry) *PeerPool {
	return &PeerPool{
		selfID: selfID,
		token:  token,
		reg:    reg,
		dialer: &websocket.Dialer{
			HandshakeTimeout: peerLinkHandshakeTimeout,
			// No Proxy: a peer link is replica-to-replica inside one
			// deployment, and honouring HTTP_PROXY would route it through
			// whatever egress proxy the environment happens to name.
		},
		links: map[string]*peerLink{},
	}
}

// Open returns a stream to peerID, dialling and caching the session on first
// use.
//
// The errors are three distinct conditions and callers act differently on
// them: ErrInstanceNotFound means the peer is not part of this deployment,
// ErrPeerUnreachable means it is but will not answer, and ErrPoolClosed means
// this process is shutting down. Only the first is node absence.
func (p *PeerPool) Open(ctx context.Context, peerID string) (net.Conn, error) {
	l, err := p.link(peerID)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.sess != nil {
		st, err := l.sess.OpenStream(ctx)
		if err == nil {
			return st, nil
		}
		// A caller whose own context expired must not cost every other worker
		// its link: the session is fine, this request is not.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// A session that died between calls is the common case, not an
		// exception, so this is a debug line and not a warning.
		xlog.Debug("cluster peer link session unusable, re-dialling", "peer", peerID, "error", err)
		_ = l.sess.Close()
		l.sess = nil
	}

	sess, err := p.dial(ctx, peerID)
	if err != nil {
		// Same rule as above, on the path that has no cached session to
		// protect: a dial that ran out of the caller's time says nothing about
		// the peer, which may be listening and perfectly healthy. Blaming it
		// would let one impatient client get a good replica routed around.
		//
		// This also swallows a genuine ErrInstanceNotFound when the context
		// happened to expire at the same moment, which is the safe direction:
		// a timeout must never be able to manufacture absence.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	st, err := sess.OpenStream(ctx)
	if err != nil {
		// The peer answered and completed a handshake but will not carry a
		// stream, which is a transport condition and never absence.
		_ = sess.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, unreachablePeer(peerID, err)
	}

	l.sess = sess
	return st, nil
}

// link returns the per-peer entry, creating it on first use.
//
// Entries are never pruned: a peer id opened once keeps its entry, and any
// session cached on it, until Close. The cost is not the map entry. A peer that
// has left the deployment but is still listening keeps a live WebSocket and the
// two yamux loop goroutines behind it for as long as this process runs; a peer
// that is genuinely gone is reclaimed by the 30s keepalive default, so the real
// exposure is narrow. There is no Forget: the membership sweep DELETES departed
// replicas but reports only how many, so which ones they were would have to be
// surfaced before anything could be plumbed here. Until it is, an entry for a
// departed peer outlives it and only the keepalive reclaims what it holds.
func (p *PeerPool) link(peerID string) (*peerLink, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, ErrPoolClosed
	}
	l, ok := p.links[peerID]
	if !ok {
		l = &peerLink{}
		p.links[peerID] = l
	}
	return l, nil
}

// dial resolves the peer's advertised address and brings up one yamux client
// session over an authenticated WebSocket.
//
// A registry miss is returned unchanged so ErrInstanceNotFound reaches the
// caller; everything after it is wrapped as unreachable.
//
// The address is only read here, so a peer that re-registers on a new address
// while its current session is still alive keeps being reached over that
// session until it dies. That is deliberate: an address change without a
// session break means the peer is still answering on the old one, and dropping
// a working link to chase a registry write would interrupt live requests for
// nothing. A replica that actually moved breaks its sessions in the process,
// and the re-dial above picks the new address up on the next Open.
func (p *PeerPool) dial(ctx context.Context, peerID string) (*yamux.Session, error) {
	inst, err := p.reg.Get(ctx, peerID)
	if err != nil {
		return nil, err
	}
	if inst.AdvertisedAddr == "" {
		// A registered replica with no address is reachable by nobody. It is
		// present, so this is not absence.
		return nil, unreachablePeer(peerID, errors.New("peer has no advertised address"))
	}

	endpoint := url.URL{
		// Plain ws: replica-to-replica TLS is not part of this phase, and the
		// link is authenticated by the cluster token rather than by transport.
		Scheme:   "ws",
		Host:     inst.AdvertisedAddr,
		Path:     PeerPath,
		RawQuery: url.Values{"id": []string{p.selfID}}.Encode(),
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.token)

	ws, resp, err := p.dialer.DialContext(ctx, endpoint.String(), header)
	if resp != nil && resp.Body != nil {
		// gorilla hands back the failed handshake's response so a caller can
		// read the status; nothing here needs the body, but it has to be
		// drained or the connection is not returned to the transport.
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, unreachablePeer(peerID, err)
	}

	// Client side of the mux: the dialling replica owns the odd stream IDs,
	// matching the yamux.Server the peer handler puts on its end.
	sess, err := yamux.Client(WebsocketConn(ws), peerLinkConfig(), nil)
	if err != nil {
		_ = ws.Close()
		return nil, unreachablePeer(peerID, err)
	}

	// Close raced this dial. Handing the session back would leak it, since
	// Close has already walked the map.
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		_ = sess.Close()
		return nil, ErrPoolClosed
	}

	xlog.Debug("cluster peer link dialled", "peer", peerID, "addr", inst.AdvertisedAddr)
	return sess, nil
}

// Close closes every cached session. It is safe to call twice, and an Open
// after it reports ErrPoolClosed rather than anything a caller could read as
// node absence.
func (p *PeerPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	links := p.links
	p.links = nil
	p.mu.Unlock()

	// Each session is closed under its own peer lock rather than under p.mu,
	// so closing the pool cannot deadlock against an Open that is mid-dial and
	// about to take p.mu to re-check p.closed.
	for _, l := range links {
		l.mu.Lock()
		if l.sess != nil {
			_ = l.sess.Close()
			l.sess = nil
		}
		l.mu.Unlock()
	}
}
