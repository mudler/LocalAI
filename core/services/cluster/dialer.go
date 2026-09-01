// SPDX-License-Identifier: MIT

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mudler/xlog"
)

// PeerOpener opens a stream to another frontend replica. *PeerPool is the
// production implementation.
//
// It is an interface so a spec can decide what a peer does without standing up
// a second frontend. Note that a TYPED nil (a (*PeerPool)(nil) stored here)
// would not compare equal to nil and would be called; the dialer's nil check is
// for the untyped nil a deployment with no peer mesh passes.
type PeerOpener interface {
	Open(ctx context.Context, peerID string) (net.Conn, error)
}

// ErrNoRelayPath reports that a worker's tunnel is held by ANOTHER replica and
// this one has no way to reach that replica.
//
// It is its own condition, kept out of the four this phase refuses to collapse.
// Not ErrNotOwner, because that tells a caller to resolve the owner again and
// the answer would not change. Not ErrPeerUnreachable, because no peer was
// dialled and none refused; saying otherwise would blame a replica that is
// probably fine. And above all not ErrNoConnection: the worker IS connected,
// and core/services/nodes reclaims the models of a worker it believes absent.
var ErrNoRelayPath = errors.New("cluster: this replica cannot relay to the owner of that worker")

// dialHandshakeTimeout bounds the request/reply exchange that opens every
// stream, when the caller stated no deadline of its own.
//
// It is a backstop and not a budget. A worker or a relay that accepted a stream
// and then said nothing would otherwise park the caller until the session's
// keepalive killed it, which is 30 seconds on the yamux default and longer if a
// deployment ever raises it. Where the caller DOES carry a deadline, that
// deadline is used instead whenever it is the shorter of the two, for the same
// reason the relay takes the smaller of its ceiling and the stated budget.
const dialHandshakeTimeout = 15 * time.Second

// WorkerDialer opens connections to a worker through its tunnel, wherever in
// the deployment that tunnel happens to be held.
//
// It is the single door: a worker holds ONE tunnel, it lands on ONE frontend
// replica, and nothing else in the frontend may dial a worker's advertised
// address. Every protocol the frontend speaks to a worker (gRPC to a backend
// process, HTTP for file staging and logs, a WebSocket for log streaming) goes
// through the functions below, because a worker behind NAT has no address to
// dial and a worker that has one must not be reached that way either: a direct
// dial works in a single-replica test and fails in production.
type WorkerDialer struct {
	// Both are read off the tunnel registry rather than passed separately, so
	// the identity this dialer compares owners against is by construction the
	// identity the registry CLAIMS as. Two ids here would make this replica
	// relay to itself for every worker it holds.
	tunnels *TunnelRegistry
	peers   PeerOpener
}

// NewWorkerDialer returns the dialer for the tunnels this replica holds and the
// peer links it can relay over. A nil peers means this replica cannot relay,
// which is reported as ErrNoRelayPath rather than as a worker being absent.
func NewWorkerDialer(tunnels *TunnelRegistry, peers PeerOpener) *WorkerDialer {
	return &WorkerDialer{tunnels: tunnels, peers: peers}
}

// Dial opens one stream to a local service on a worker: tag says which service
// (see StreamTagGRPC and StreamTagHTTP) and target which instance of it.
//
// The returned conn is past both handshakes and carries the tunnelled protocol
// and nothing else, with no deadline armed on it: what follows may be an
// inference that is quiet for minutes, and a deadline left over from the
// handshake would abort it.
//
// The errors are kept apart on purpose and a caller may act on them
// differently. ErrNoConnection means no live replica holds this worker's
// tunnel, which is the one answer that means the worker is absent. ErrNotOwner
// means the routing was stale and re-resolving may find it. ErrPeerUnreachable
// means a replica would not answer, ErrNoRelayPath that none could be dialled,
// and the tunnelproto sentinels that the worker itself refused. Nothing here
// ever converts one of the others into absence.
func (d *WorkerDialer) Dial(ctx context.Context, nodeID, tag, target string) (net.Conn, error) {
	stream, err := d.tunnels.Open(ctx, nodeID)
	if err == nil {
		return d.handshake(ctx, stream, nodeID, tag, target)
	}
	if !errors.Is(err, ErrNotOwner) {
		// The tunnel is held HERE and its session would not carry a stream.
		// Reported as itself: answering ErrNotOwner would send the caller to
		// resolve an owner that is this same replica, and answering absence
		// would tell a scheduler to reclaim a worker that is attached.
		return nil, err
	}
	return d.relay(ctx, nodeID, tag, target)
}

// DialerFor returns a net.Dialer-shaped function bound to one worker and one
// local service on it.
//
// This is the shape http.Transport.DialContext and websocket.Dialer's
// NetDialContext want. The NETWORK is ignored and the ADDRESS becomes the
// stream's target, which is what makes an http.Client or a WebSocket dialler
// built on it reach the worker without either of them knowing a tunnel exists:
// the URL still names the worker's registered address, and that address travels
// as the target rather than to a socket. What the worker does with it is the
// worker's decision (the grpc tag takes the port and dials its own loopback,
// the http tag ignores it entirely), which is the property that stops a
// frontend from steering a worker's dial.
func (d *WorkerDialer) DialerFor(nodeID, tag string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.Dial(ctx, nodeID, tag, addr)
	}
}

// GRPCDialerFor returns a grpc.WithContextDialer-shaped function bound to one
// worker's backend processes. gRPC's dialer takes no network argument, which is
// why this is not DialerFor's shape.
func (d *WorkerDialer) GRPCDialerFor(nodeID string) func(ctx context.Context, addr string) (net.Conn, error) {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return d.Dial(ctx, nodeID, StreamTagGRPC, addr)
	}
}

// relay opens the stream through the replica that holds the worker's tunnel.
func (d *WorkerDialer) relay(ctx context.Context, nodeID, tag, target string) (net.Conn, error) {
	// Owner, never OwnerRow: the row outlives its owner by up to a liveness
	// window plus a heartbeat, and dialling what the unjoined read returns
	// means dialling a process that is gone and reporting the worker as
	// unreachable rather than as absent. The join is what makes a dead owner
	// come back as ErrNoConnection here.
	owner, _, err := d.tunnels.reg.Owner(ctx, nodeID)
	if err != nil {
		// ErrNoConnection and database failures both pass through as
		// themselves. This is the ONLY path by which this function can produce
		// an absence error, and it produces it only when Owner did.
		return nil, err
	}
	if owner == d.tunnels.selfID {
		// The table names this replica and the registry above said the tunnel
		// is not held here, so the attachment went away between the claim and
		// now. Relaying would send the request into this same process, which
		// would resolve the same owner and relay again. Reported as the routing
		// fact so the caller re-resolves, which terminates: the row is either
		// re-claimed by whoever holds the worker now, or swept.
		return nil, fmt.Errorf("opening a stream to node %q: the connection row names this replica, which no longer holds the tunnel: %w", nodeID, ErrNotOwner)
	}
	if d.peers == nil {
		return nil, fmt.Errorf("opening a stream to node %q held by replica %q: %w", nodeID, owner, ErrNoRelayPath)
	}

	stream, err := d.peers.Open(ctx, owner)
	if err != nil {
		// Whatever the pool said, unchanged in its unwrap chain:
		// ErrPeerUnreachable, ErrInstanceNotFound for an owner swept since the
		// lookup above, or ErrPoolClosed while this process shuts down. None of
		// them is a statement about the WORKER, and none is converted into one.
		return nil, fmt.Errorf("opening a stream to node %q through replica %q: %w", nodeID, owner, err)
	}

	// The caller's remaining time, stated so the owning replica can bound its
	// own open by it. Only this side knows it; see relayOpenTimeout for what
	// the owner falls back to without it.
	if err := WriteRelayRequest(stream, nodeID, remainingBudget(ctx)); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("naming node %q on a stream to replica %q: %w", nodeID, owner, err)
	}
	if err := ReadRelayReply(stream); err != nil {
		// ReadRelayReply already separates a refusal (ErrNotOwner,
		// ErrRelayUnavailable, ErrRelayRequestInvalid) from a failure to read
		// one, and neither kind is ever an absence error.
		_ = stream.Close()
		return nil, fmt.Errorf("relaying to node %q through replica %q: %w", nodeID, owner, err)
	}
	return d.handshake(ctx, stream, nodeID, tag, target)
}

// handshake names the worker-side service on a stream and waits for the
// worker's answer, leaving the stream ready for the tunnelled protocol.
//
// It owns closing the stream on every failure. A stream left open after a
// failed handshake holds a yamux slot on the tunnel for the life of the
// session, and a frontend that retries would exhaust the worker's stream
// budget rather than the worker's patience.
func (d *WorkerDialer) handshake(ctx context.Context, stream net.Conn, nodeID, tag, target string) (net.Conn, error) {
	if deadline, ok := handshakeDeadline(ctx); ok {
		if err := stream.SetDeadline(deadline); err != nil {
			_ = stream.Close()
			return nil, fmt.Errorf("arming the handshake deadline for node %q: %w", nodeID, err)
		}
	}

	if err := WriteStreamRequest(stream, tag, target); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("asking node %q for %q on %q: %w", nodeID, tag, target, err)
	}
	if err := ReadStreamReply(stream); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("opening %q on node %q: %w", tag, nodeID, err)
	}

	// Cleared unconditionally rather than only when one was armed, so that this
	// stays true of the stream whatever the caller's context carried. What
	// follows is the caller's protocol, and its length is the caller's
	// business: a request may sit quiet for minutes between tokens, and the
	// handshake's deadline would end it. The session's keepalive is what still
	// bounds a peer that has stopped answering.
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("clearing the handshake deadline for node %q: %w", nodeID, err)
	}
	xlog.Debug("opened a tunnelled stream to a worker", "node", nodeID, "tag", tag, "target", target)
	return stream, nil
}

// handshakeDeadline is when the handshake must be done by: the caller's own
// deadline when it has one and it is the sooner, and the backstop otherwise.
func handshakeDeadline(ctx context.Context) (time.Time, bool) {
	backstop := time.Now().Add(dialHandshakeTimeout)
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(backstop) {
		return backstop, true
	}
	return deadline, true
}

// remainingBudget is how long the caller is still willing to wait, or zero when
// it did not say.
//
// Zero rather than a negative number for an expired context: the frame writer
// treats zero as "not stated", and a caller that has already run out is about
// to fail on its own context anyway. Stating a negative budget would instead
// make the owning replica refuse, which is the same outcome by a longer route.
func remainingBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	return remaining
}
