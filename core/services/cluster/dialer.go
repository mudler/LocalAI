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

// ErrNoRoute reports that this replica could not get a request to a worker's
// backend, and is the FIFTH condition this phase keeps apart.
//
// It says nothing about whether the worker exists or is running. A worker's
// PRESENCE is its heartbeat, which lives in core/services/nodes and which this
// package cannot see; what this package can see is whether a route exists right
// now, and those are different questions with different answers. A worker that
// is registered, heartbeating and serving models can be unroutable from here
// for a whole list of ordinary reasons: it has not dialled its tunnel yet after
// a frontend-first upgrade, the replica holding its tunnel is restarting, the
// ownership row is a moment stale, this replica has no peer mesh.
//
// Every failure to RESOLVE OR OPEN a route carries it, so a consumer that must
// not act on absence has exactly one check to make. The specific condition
// stays in the unwrap chain underneath for anyone that can act on it, with one
// deliberate exception: see routeFailure.
//
// It is not carried by a REFUSAL from the worker itself. A worker that answers
// is present by demonstration, and folding its answer into "no route" would
// throw away the one thing on this path that is real evidence.
var ErrNoRoute = errors.New("cluster: no route from this replica to that worker")

// noRouteError reports a worker this replica cannot route to, keeping the cause
// in its message and OUT of its unwrap chain.
//
// Withholding the cause is the entire point, and it is the same guarantee
// unreachableError makes for peers. The causes this is built over are absence
// claims: ErrNoConnection ("no live replica holds this worker's tunnel") and
// ErrInstanceNotFound ("no such frontend replica"). Both are true statements
// about the CLUSTER and neither is a statement about the worker, but a consumer
// matching on them would read them as one, and the consequence is the
// catastrophe this phase is built around: a scheduler concludes a worker that
// is heartbeating and serving has gone away, and reclaims its models.
//
// The guarantee therefore belongs to the type. There is no path by which an
// absence sentinel gets out, so no call site can leak one.
type noRouteError struct {
	nodeID string
	cause  error
}

func (e *noRouteError) Error() string {
	return fmt.Sprintf("cluster: no route from this replica to node %q: %v", e.nodeID, e.cause)
}

// Unwrap reports only ErrNoRoute. The cause reaches a human through Error() and
// reaches no error-matching caller at all.
func (e *noRouteError) Unwrap() error { return ErrNoRoute }

// routeFailure is the ONE place a Dial failure is turned into an error, and the
// one place the absence rule is expressed.
//
// The rule: an absence claim never reaches a caller, and everything else stays
// matchable. It is a single predicate in a single function on purpose. An
// earlier shape in this phase encoded one policy in two predicates, and
// reverting either left the suite green because the error reached the same
// answer down the other path; a rule whose correctness argument IS its mutation
// evidence cannot afford to be un-mutatable in pieces. Falsifying either half
// of isAbsenceClaim now reddens a named spec.
func routeFailure(nodeID string, cause error) error {
	if isAbsenceClaim(cause) {
		return &noRouteError{nodeID: nodeID, cause: cause}
	}
	return fmt.Errorf("reaching node %q: %w: %w", nodeID, ErrNoRoute, cause)
}

// isAbsenceClaim reports whether an error asserts that something does not
// exist. Those are the errors routeFailure keeps out of the chain.
//
// Both are about the CLUSTER rather than about the worker. ErrNoConnection says
// no live replica holds the worker's tunnel; ErrInstanceNotFound says a peer
// replica is not in the deployment. Neither can be answered by this package
// with "and therefore the worker is gone", because this package does not know
// what a worker is beyond an id in a connection row.
func isAbsenceClaim(err error) bool {
	return errors.Is(err, ErrNoConnection) || errors.Is(err, ErrInstanceNotFound)
}

// IsWorkerAnswer reports whether an error is the WORKER's own refusal, read off
// the reply it sent.
//
// Those three sentinels are the only ones ReadStreamReply produces from a frame
// the worker actually wrote. Everything else it returns is a failure to read
// one, which is the tunnel breaking rather than the worker speaking.
//
// A reply carrying a code this frontend does not recognise is deliberately NOT
// counted here, even though a worker did send it. Classifying it as an answer
// would let a newer worker's vocabulary be read by an older frontend as
// evidence about a backend, and the consequence of guessing wrong in that
// direction is a reaped replica; guessing wrong the other way costs a retry.
//
// It is EXPORTED because it is half of a contract, not an implementation
// detail. Dial keeps these three out of the ErrNoRoute umbrella so that a
// consumer can act on them; a consumer that cannot ask "was this the worker
// speaking?" has no way to use that, and for a whole phase none could, so every
// worker refusal reached the schedulers as "this frontend has no route" and
// nothing could ever be reaped. The two sides must agree on the SAME set, so
// there is one predicate and both call it: see nodes.unroutable and
// model.transportFailure, whose job is to answer "did this call reach a
// backend?" and for whom a refusal means it did.
func IsWorkerAnswer(err error) bool {
	return errors.Is(err, ErrStreamTagUnknown) ||
		errors.Is(err, ErrStreamTargetUnavailable) ||
		errors.Is(err, ErrStreamRequestInvalid)
}

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
// EVERY failure to resolve or open a route carries ErrNoRoute, and NO failure
// carries an absence sentinel. That pair is the contract, and it is what makes
// this safe to consume from a package that reclaims a worker's models when it
// decides the worker has gone: there is one check to make, and there is nothing
// to mistake for absence even if the caller makes none.
//
// Underneath the umbrella the conditions stay apart and a caller may act on
// them differently. ErrNotOwner means the routing was stale and re-resolving
// may find it; ErrPeerUnreachable means a replica would not answer;
// ErrNoRelayPath means none could be dialled. A refusal from the WORKER carries
// its own tunnelproto sentinel and no umbrella at all, because a worker that
// answers has demonstrated it is there.
func (d *WorkerDialer) Dial(ctx context.Context, nodeID, tag, target string) (net.Conn, error) {
	stream, err := d.tunnels.Open(ctx, nodeID)
	if err == nil {
		return d.handshake(ctx, stream, nodeID, tag, target)
	}
	if !errors.Is(err, ErrNotOwner) {
		// The tunnel is held HERE and its session would not carry a stream.
		// ErrNotOwner stays out of it: that answer would send the caller to
		// resolve an owner which is this same replica.
		return nil, routeFailure(nodeID, err)
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
		// ErrNoConnection is the ordinary answer here, and it is precisely the
		// one that must not get out: it means no live replica holds this
		// worker's tunnel, which a worker that has not dialled in yet produces
		// on every single request while it sits there heartbeating and serving.
		// routeFailure keeps it in the message and out of the chain.
		return nil, routeFailure(nodeID, err)
	}
	if owner == d.tunnels.selfID {
		// The table names this replica and the registry above said the tunnel
		// is not held here, so the attachment went away between the claim and
		// now. Relaying would send the request into this same process, which
		// would resolve the same owner and relay again. Reported as the routing
		// fact so the caller re-resolves, which terminates: the row is either
		// re-claimed by whoever holds the worker now, or swept.
		return nil, routeFailure(nodeID, fmt.Errorf("the connection row names this replica, which no longer holds the tunnel: %w", ErrNotOwner))
	}
	if d.peers == nil {
		return nil, routeFailure(nodeID, fmt.Errorf("the tunnel is held by replica %q: %w", owner, ErrNoRelayPath))
	}

	stream, err := d.peers.Open(ctx, owner)
	if err != nil {
		// ErrPeerUnreachable and ErrPoolClosed keep their identity; the one
		// case the pool can also produce, ErrInstanceNotFound for an owner
		// swept between the lookup above and this dial, is an absence claim
		// about the REPLICA and routeFailure withholds it. Either way nothing
		// here is a statement about the worker.
		return nil, routeFailure(nodeID, fmt.Errorf("through replica %q: %w", owner, err))
	}

	// The caller's remaining time, stated so the owning replica can bound its
	// own open by it. Only this side knows it; see relayOpenTimeout for what
	// the owner falls back to without it.
	if err := WriteRelayRequest(stream, nodeID, remainingBudget(ctx)); err != nil {
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("naming the node on a stream to replica %q: %w", owner, err))
	}
	if err := ReadRelayReply(stream); err != nil {
		// A refusal from the OWNING REPLICA, not from the worker. It says the
		// owner would not relay, which is a route that does not exist, so the
		// umbrella is right for all of them; ErrNotOwner, ErrRelayUnavailable
		// and ErrRelayRequestInvalid stay in the chain underneath.
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("through replica %q: %w", owner, err))
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
	// blameCaller attributes a handshake I/O failure to the CALLER's own spent
	// budget when that is what ended it, and returns nil when it was not.
	//
	// The third instance of the rule peerlink.go states in full at callerRanOut:
	// the handshake deadline IS the caller's deadline whenever the caller's is
	// the shorter (see handshakeDeadline), so a caller that has run out makes
	// the socket's own timer fire, and the resulting i/o timeout arrives here
	// while ctx.Err() may still read nil because nothing orders the two timers.
	// Reported plainly, that is "the tunnel would not carry the request" for a
	// worker that is connected, healthy and idle.
	//
	// The umbrella stays on either way, so Dial's contract is unchanged; what
	// changes is that context.DeadlineExceeded is matchable underneath and the
	// log line names the caller instead of the worker. It deliberately runs
	// BEFORE the worker-answer check below, so a refusal that arrived in the
	// same instant the budget expired is reported as the caller's timeout: a
	// spent deadline must never be able to manufacture evidence about a
	// backend, which is the same direction peerlink.go takes for absence.
	blameCaller := func(what string) error {
		ctxErr := callerRanOut(ctx)
		if ctxErr == nil {
			return nil
		}
		return routeFailure(nodeID, fmt.Errorf("%s: the caller's own budget ran out: %w", what, ctxErr))
	}

	if err := stream.SetDeadline(handshakeDeadline(ctx)); err != nil {
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("arming the handshake deadline: %w", err))
	}

	if err := WriteStreamRequest(stream, tag, target); err != nil {
		// The stream would not carry the request, so the tunnel broke under it.
		// Nothing was asked of the worker and nothing was learned about it.
		_ = stream.Close()
		if blamed := blameCaller(fmt.Sprintf("asking for %q on %q", tag, target)); blamed != nil {
			return nil, blamed
		}
		return nil, routeFailure(nodeID, fmt.Errorf("asking for %q on %q: %w", tag, target, err))
	}
	if err := ReadStreamReply(stream); err != nil {
		_ = stream.Close()
		if blamed := blameCaller(fmt.Sprintf("opening %q", tag)); blamed != nil {
			return nil, blamed
		}
		if IsWorkerAnswer(err) {
			// The worker wrote a refusal, so it is connected and answering.
			// This is the ONE failure on the whole path that is real evidence
			// about the worker, and putting the umbrella on it would throw that
			// away.
			return nil, fmt.Errorf("opening %q on node %q: %w", tag, nodeID, err)
		}
		return nil, routeFailure(nodeID, fmt.Errorf("opening %q: %w", tag, err))
	}

	// Cleared unconditionally rather than only when one was armed, so that this
	// stays true of the stream whatever the caller's context carried. What
	// follows is the caller's protocol, and its length is the caller's
	// business: a request may sit quiet for minutes between tokens, and the
	// handshake's deadline would end it. The session's keepalive is what still
	// bounds a peer that has stopped answering.
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("clearing the handshake deadline: %w", err))
	}
	xlog.Debug("opened a tunnelled stream to a worker", "node", nodeID, "tag", tag, "target", target)
	return stream, nil
}

// handshakeDeadline is when the handshake must be done by: the caller's own
// deadline when it has one and it is the sooner, and the backstop otherwise.
//
// There is always one, which is why this returns no "was there one" flag: a
// context with no deadline still gets the backstop, so the caller has nothing
// to branch on. It used to return a bool that was unconditionally true, and the
// branch behind it could not be taken.
func handshakeDeadline(ctx context.Context) time.Time {
	backstop := time.Now().Add(dialHandshakeTimeout)
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(backstop) {
		return backstop
	}
	return deadline
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
