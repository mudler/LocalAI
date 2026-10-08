package tunnel

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
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// PeerPath is the route that a replica dials to open a peer link, and the route
// that the HTTP layer registers the handler on. The literal is spelled out here
// and not derived from the auth package, so that this package does not import
// it. A spec in the endpoints package, which sees both, keeps them equal.
const PeerPath = "/api/cluster/peer"

// ErrPeerUnreachable reports that a peer which this deployment knows could not be
// reached: the dial failed, the peer refused the credentials, or its multiplexer
// would not carry a stream.
//
// It is not a form of ErrInstanceNotFound, and the two must stay apart. A caller
// that sees absence may conclude that a node is gone and reclaim what it ran. A
// caller that sees unreachability may only retry. It is not ErrNoRoute either:
// a link between two frontends is not a fact about any worker. See
// unreachableError for how this is enforced and not only documented.
var ErrPeerUnreachable = errors.New("tunnel: peer unreachable")

// unreachableError reports a peer that could not be reached, with the cause in
// its message and out of its unwrap chain. The dial resolves the peer through the
// registry, so ErrInstanceNotFound is a cause this can be built over, and an
// unwrapped one would satisfy an absence check on a transport problem.
type unreachableError struct {
	peerID string
	cause  error
}

func (e *unreachableError) Error() string {
	return fmt.Sprintf("tunnel: peer %q unreachable: %v", e.peerID, e.cause)
}

// Unwrap reports only ErrPeerUnreachable.
func (e *unreachableError) Unwrap() error { return ErrPeerUnreachable }

func unreachablePeer(peerID string, cause error) error {
	return &unreachableError{peerID: peerID, cause: cause}
}

// ErrPeerRejected reports that a peer answered and refused the credential of this
// replica. It has a different cure from the other ways a dial can end: a peer
// that does not answer is a transport problem that an operator waits out, and a
// peer that says no is a configuration problem.
//
// It is not absence. It says that a peer is up and talking.
var ErrPeerRejected = errors.New("tunnel: peer refused the credential of this replica")

// rejectedError reports a peer that refused the dial. It unwraps to
// ErrPeerRejected and to ErrPeerUnreachable: the second keeps every caller that
// already handles an unreachable peer correct for a refusal, and the first lets a
// caller that wants to tell them apart do so. It unwraps to no absence sentinel.
type rejectedError struct {
	peerID string
	cause  error
}

func (e *rejectedError) Error() string {
	return fmt.Sprintf("tunnel: peer %q refused the credential of this replica: %v", e.peerID, e.cause)
}

func (e *rejectedError) Unwrap() []error { return []error{ErrPeerRejected, ErrPeerUnreachable} }

func rejectedPeer(peerID string, cause error) error {
	return &rejectedError{peerID: peerID, cause: cause}
}

// ErrPoolClosed reports an Open on a pool that was shut down. It is a fact about
// this process and says nothing about whether the peer exists.
var ErrPoolClosed = errors.New("tunnel: peer pool is closed")

// peerLinkHandshakeTimeout bounds the websocket upgrade of a peer link. It also
// bounds how long Close waits behind a dial in progress, which holds the lock of
// its peer.
const peerLinkHandshakeTimeout = 10 * time.Second

// PeerPool dials peer replicas and keeps one multiplexed session for each.
//
// A peer link carries the traffic of every worker that the peer owns, so it is
// pooled: a dial for each relayed request would add a websocket handshake to
// every inference.
//
// The pool needs no knowledge of the errors of the multiplexer to keep its cache
// honest. A peer that shut down hands its session a go-away, and a session whose
// transport died hands out its shutdown error, and both arrive as a failed
// OpenStream that the same retry handles. A condition of one stream, such as a
// peer that reset one request, never reaches the pool, and dropping the session
// for it would tear down every other worker on the link.
type PeerPool struct {
	selfID string
	// cred is the own peer credential of this replica. It is what makes selfID a
	// claim and not a label: the peer resolves selfID to a row and checks this
	// against the hash that the row publishes.
	cred PeerCredential
	reg  *cluster.Registry

	dialer *websocket.Dialer

	mu     sync.Mutex
	links  map[string]*peerLink
	closed bool
}

// PeerCredential is the credential of a replica. It is cluster.PeerCredential;
// the alias keeps the callers of this package from importing both.
type PeerCredential = cluster.PeerCredential

// peerLink is the cached session for one peer and the lock that serialises
// dialling it. The lock is per peer, so a dial to one peer that hangs does not
// hold up the opens to another.
type peerLink struct {
	mu   sync.Mutex
	sess *Session
}

// NewPeerPool returns a pool that dials peers as selfID. cred must be the value
// that the membership loop published the hash of, which makes a pool that
// presents one secret and a row that holds the hash of another unstateable.
//
// The pool authenticates with the credential of the replica and with nothing
// else. The registration token of the deployment opens no peer link, and an empty
// token therefore fails no peer dial.
func NewPeerPool(selfID string, cred PeerCredential, reg *cluster.Registry) *PeerPool {
	return &PeerPool{
		selfID: selfID,
		cred:   cred,
		reg:    reg,
		// No proxy: a peer link is inside one deployment, and honouring HTTP_PROXY
		// would route it through whatever egress proxy the environment names.
		dialer: NewDialer(peerLinkHandshakeTimeout),
		links:  map[string]*peerLink{},
	}
}

// Open returns a stream to peerID, and dials and caches the session on first use.
//
// The errors are three conditions and callers act on them differently:
// ErrInstanceNotFound means the peer is not part of the deployment,
// ErrPeerUnreachable means it is and does not answer, and ErrPoolClosed means
// that this process is shutting down. Only the first is absence.
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
		// A caller whose own budget ran out must not cost every other worker its
		// link: the session is well, and this request is not.
		if ctxErr := callerRanOut(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		// A session that died between calls is the common case.
		xlog.Debug("tunnel peer link session unusable, dialling again", "peer", peerID, "error", err)
		_ = l.sess.Close()
		l.sess = nil
	}

	sess, err := p.dial(ctx, peerID)
	if err != nil {
		// A dial that ran out of the time of the caller says nothing about the
		// peer, which may be well. This also swallows a real ErrInstanceNotFound
		// when the budget ran out in the same moment, which is the safe direction:
		// a timeout must never manufacture absence.
		if ctxErr := callerRanOut(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}

	st, err := sess.OpenStream(ctx)
	if err != nil {
		// The peer answered and completed the handshake and will not carry a
		// stream. That is a condition of the transport and never absence.
		_ = sess.Close()
		if ctxErr := callerRanOut(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, unreachablePeer(peerID, err)
	}
	l.sess = sess
	return st, nil
}

// link returns the entry for a peer and creates it on first use. Entries are not
// pruned: a peer that left the deployment but still listens keeps a websocket and
// two goroutines until Close or until the keepalive reclaims the session.
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

// dial resolves the advertised address of the peer and brings up the client side
// of a session over an authenticated websocket.
//
// A miss in the registry is returned as it is, so that ErrInstanceNotFound
// reaches the caller. Everything after it is wrapped as unreachable. The address
// is read only here: a peer that registers a new address while its session is
// alive keeps being reached over that session, which is right, because the peer
// still answers on the old one.
func (p *PeerPool) dial(ctx context.Context, peerID string) (*Session, error) {
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
		// Plain ws: the link is authenticated by the credential of the replica and
		// not by the transport. A deployment that wants the link encrypted puts the
		// replicas behind TLS.
		Scheme:   "ws",
		Host:     inst.AdvertisedAddr,
		Path:     PeerPath,
		RawQuery: url.Values{"id": []string{p.selfID}}.Encode(),
	}
	header := http.Header{}
	// Sent even when empty. An empty value is refused by the peer, which is right
	// for a process that never minted a credential, and omitting the header would
	// make that indistinguishable from a release that predates it.
	header.Set(cluster.PeerIdentityHeader, p.cred.Token())

	ws, resp, err := p.dialer.DialContext(ctx, endpoint.String(), header)
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	if err != nil {
		// A peer that answered 401 is up and talking, and what it refused is the
		// credential. The log line names the cure.
		if status == http.StatusUnauthorized {
			xlog.Warn("A peer refused the credential of this replica: the credential is not the one the peer has on record for this replica id. "+
				"This replica may have registered under an id that another process uses, or its row was replaced",
				"peer", peerID, "self", p.selfID, "addr", inst.AdvertisedAddr)
			return nil, rejectedPeer(peerID, err)
		}
		return nil, unreachablePeer(peerID, err)
	}

	sess, err := peerClientSession(ws)
	if err != nil {
		_ = ws.Close()
		return nil, unreachablePeer(peerID, err)
	}

	// Close raced this dial. Handing the session back would leak it, since Close
	// has already walked the map.
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		_ = sess.Close()
		return nil, ErrPoolClosed
	}
	xlog.Debug("tunnel peer link dialled", "peer", peerID, "addr", inst.AdvertisedAddr)
	return sess, nil
}

// Close closes every cached session. It can be called twice, and an Open after it
// reports ErrPoolClosed.
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

	// Each session closes under the lock of its own peer, so that closing the pool
	// cannot deadlock against an Open that is dialling and about to take p.mu.
	for _, l := range links {
		l.mu.Lock()
		if l.sess != nil {
			_ = l.sess.Close()
			l.sess = nil
		}
		l.mu.Unlock()
	}
}
