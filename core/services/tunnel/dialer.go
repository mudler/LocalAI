package tunnel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mudler/xlog"
)

// PeerOpener opens a stream to another frontend replica. *PeerPool is the
// production implementation. It is an interface so that a spec can decide what a
// peer does without a second frontend.
type PeerOpener interface {
	Open(ctx context.Context, peerID string) (net.Conn, error)
}

// dialHandshakeTimeout bounds the request and reply that open every stream, when
// the caller stated no deadline of its own. It is a backstop and not a budget. A
// worker or a relay that accepted a stream and then said nothing would otherwise
// park the caller until the keepalive of the session killed it. A deadline of the
// caller is used instead when it is the shorter of the two.
const dialHandshakeTimeout = 15 * time.Second

// WorkerDialer opens connections to a worker through its tunnel, wherever in the
// deployment that tunnel is held.
//
// It is the single door. A worker holds one tunnel, it lands on one replica, and
// no other part of the frontend dials the address of a worker. Every protocol
// that the frontend speaks to a worker goes through here: gRPC to a backend
// process, HTTP for control and file transfer, and a WebSocket for logs. A worker
// behind NAT has no address to dial, and a direct dial that works in a test with
// one replica fails in production.
type WorkerDialer struct {
	// The registry carries both the identity of this replica and the sessions it
	// holds, so that the identity this dialer compares owners against is the one
	// the registry claims as. Two identities would make this replica relay to
	// itself for every worker it holds.
	tunnels *Registry
	peers   PeerOpener
}

// NewWorkerDialer returns the dialer for the tunnels this replica holds and the
// peer links it can relay over. A nil peers means that this replica cannot
// relay, which is reported as ErrNoRelayPath and not as a worker being absent.
func NewWorkerDialer(tunnels *Registry, peers PeerOpener) *WorkerDialer {
	return &WorkerDialer{tunnels: tunnels, peers: peers}
}

// DialOption changes how Dial picks the session of the worker.
type DialOption func(*dialOptions)

type dialOptions struct {
	lane       Lane
	noFallback bool
}

// WithBulkLane sends the stream over the bulk lane, and fails when the worker has
// no bulk session. It does not fall back to the inference lane. A file transfer
// asks for it: a transfer on the lane of model calls delays every one of them,
// which is what the bulk lane is for.
func WithBulkLane() DialOption {
	return func(o *dialOptions) { o.lane, o.noFallback = LaneBulk, true }
}

// Dial opens one stream to a local service of a worker. tag says which service
// (StreamTagGRPC or StreamTagHTTP) and target which instance of it.
//
// The connection that comes back is past both handshakes and carries the
// protocol of the caller and nothing else. No deadline is armed on it: what
// follows can be an inference that is quiet for minutes.
//
// Every failure to resolve or open a route carries ErrNoRoute, and no failure
// carries an absence sentinel. A worker that answers keeps its own refusal and no
// ErrNoRoute, because it has shown that it is there. See routeFailure for what is
// neither.
func (d *WorkerDialer) Dial(ctx context.Context, nodeID, tag, target string, opts ...DialOption) (net.Conn, error) {
	o := dialOptions{lane: LaneInference}
	for _, opt := range opts {
		opt(&o)
	}
	var openOpts []OpenOption
	if o.noFallback {
		openOpts = append(openOpts, WithoutFallback())
	}
	stream, err := d.tunnels.Open(ctx, nodeID, o.lane, openOpts...)
	if err == nil {
		return d.handshake(ctx, stream, nodeID, tag, target)
	}
	if !errors.Is(err, ErrNotOwner) {
		// The tunnel is held here and would not carry a stream. ErrNotOwner stays
		// out: it would send the caller to resolve an owner that is this replica.
		return nil, routeFailure(nodeID, err)
	}
	return d.relay(ctx, nodeID, tag, target, o)
}

// DialerFor returns a function in the shape of http.Transport.DialContext and
// websocket.Dialer.NetDialContext, bound to one worker and one local service of
// it. The network is ignored and the address becomes the target of the stream,
// which is what lets an http.Client reach the worker without knowing that a
// tunnel exists. What the worker does with the target is its own decision: the
// grpc tag keeps the port and dials its own loopback, and the http tag ignores
// the target. A frontend therefore cannot steer a dial of the worker.
func (d *WorkerDialer) DialerFor(nodeID, tag string, opts ...DialOption) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.Dial(ctx, nodeID, tag, addr, opts...)
	}
}

// GRPCDialerFor returns a function in the shape of grpc.WithContextDialer, bound
// to the backend processes of one worker.
func (d *WorkerDialer) GRPCDialerFor(nodeID string) func(ctx context.Context, addr string) (net.Conn, error) {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return d.Dial(ctx, nodeID, StreamTagGRPC, addr)
	}
}

// relay opens the stream through the replica that holds the tunnel of the worker.
func (d *WorkerDialer) relay(ctx context.Context, nodeID, tag, target string, o dialOptions) (net.Conn, error) {
	// Owner and not the row: a row outlives its owner by up to a liveness window,
	// and a dial to what the unjoined read returns would reach a process that is
	// gone and report the worker as unreachable instead of absent.
	owner, _, err := d.tunnels.reg.Owner(ctx, nodeID)
	if err != nil {
		// ErrNoConnection is the ordinary answer for a worker that has not dialled
		// in yet, and it must not get out: routeFailure keeps it in the message.
		return nil, routeFailure(nodeID, err)
	}
	if owner == d.tunnels.selfID {
		// The table names this replica, and the registry says that the tunnel is
		// not held here, so the attachment went away between the claim and now.
		// Relaying would send the request into this same process.
		return nil, routeFailure(nodeID, fmt.Errorf("the connection row names this replica, which no longer holds the tunnel: %w", ErrNotOwner))
	}
	if d.peers == nil {
		return nil, routeFailure(nodeID, fmt.Errorf("the tunnel is held by replica %q: %w", owner, ErrNoRelayPath))
	}

	stream, err := d.peers.Open(ctx, owner)
	if err != nil {
		// ErrPeerUnreachable and ErrPoolClosed keep their identity. An owner swept
		// between the lookup and this dial is an absence claim about a replica, and
		// routeFailure withholds it.
		return nil, routeFailure(nodeID, fmt.Errorf("through replica %q: %w", owner, err))
	}

	// The relay frame and its reply run under the budget of the caller. An owner
	// whose session still answers the keepalive and whose handler is stuck would
	// otherwise hold the caller, a goroutine and a stream for as long as the link
	// lives. The deadline covers a caller that stated one, and the watcher covers
	// a caller that cancels without one.
	if err := stream.SetDeadline(handshakeDeadline(ctx)); err != nil {
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("arming the relay deadline: %w", err))
	}
	release := closeWhenDone(ctx, stream)
	defer func() { _ = release() }()

	// The remaining time of the caller, so that the owner can bound its own open.
	if err := WriteRelayRequest(stream, nodeID, relayLane(o), remainingBudget(ctx)); err != nil {
		_ = stream.Close()
		if blamed := callerRanOut(ctx); blamed != nil {
			return nil, routeFailure(nodeID, fmt.Errorf("through replica %q: the budget of the caller ran out: %w", owner, blamed))
		}
		return nil, routeFailure(nodeID, fmt.Errorf("naming the node on a stream to replica %q: %w", owner, err))
	}
	if err := ReadRelayReply(stream); err != nil {
		// A refusal of the owning replica and not of the worker: the owner would
		// not relay, which is a route that does not exist.
		_ = stream.Close()
		if blamed := callerRanOut(ctx); blamed != nil {
			return nil, routeFailure(nodeID, fmt.Errorf("through replica %q: the budget of the caller ran out: %w", owner, blamed))
		}
		return nil, routeFailure(nodeID, fmt.Errorf("through replica %q: %w", owner, err))
	}
	return d.handshake(ctx, stream, nodeID, tag, target)
}

// handshake names the service of the worker on a stream and waits for the answer
// of the worker. It owns closing the stream on every failure: a stream left
// open after a failed handshake holds a slot of the session for as long as the
// session lives.
func (d *WorkerDialer) handshake(ctx context.Context, stream net.Conn, nodeID, tag, target string) (net.Conn, error) {
	// The budget of the caller is checked first, before anything reads as the
	// answer of a worker. The deadline of the handshake is the deadline of the
	// caller when that is shorter, so a caller that runs out makes the timer of the
	// socket fire, and the timeout arrives here while ctx.Err may still be nil.
	// A refusal that arrives in the same instant is the timeout of the caller, and
	// never evidence about a backend.
	blameCaller := func(what string) error {
		ctxErr := callerRanOut(ctx)
		if ctxErr == nil {
			return nil
		}
		return routeFailure(nodeID, fmt.Errorf("%s: the budget of the caller ran out: %w", what, ctxErr))
	}

	if err := stream.SetDeadline(handshakeDeadline(ctx)); err != nil {
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("arming the handshake deadline: %w", err))
	}
	release := closeWhenDone(ctx, stream)
	defer func() { _ = release() }()

	if err := WriteStreamRequest(stream, tag, target); err != nil {
		// The tunnel broke under the request. Nothing was asked of the worker and
		// nothing was learned about it.
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
			// The worker wrote a refusal, so it is connected and answering. This is
			// the one failure on the path that is evidence about the worker, and the
			// umbrella would throw it away.
			return nil, fmt.Errorf("opening %q on node %q: %w", tag, nodeID, err)
		}
		return nil, routeFailure(nodeID, fmt.Errorf("opening %q: %w", tag, err))
	}

	// Cleared whatever the caller's context carried. What follows is the protocol
	// of the caller, and a deadline of the handshake would end it.
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = stream.Close()
		return nil, routeFailure(nodeID, fmt.Errorf("clearing the handshake deadline: %w", err))
	}
	if release() {
		// The caller gave up in the last moment and the watch closed the stream.
		return nil, routeFailure(nodeID, fmt.Errorf("opening %q: the budget of the caller ran out: %w", tag, cmp.Or(callerRanOut(ctx), context.Canceled)))
	}
	xlog.Debug("opened a tunnelled stream to a worker", "node", nodeID, "tag", tag, "target", target)
	return stream, nil
}

// closeWhenDone closes the stream when ctx ends, so that a read or a write that
// is parked on a peer which says nothing returns at once. The deadline of the
// socket covers a caller that stated one; this covers a caller that only
// cancels. The returned function stops the watch and must be called when the
// exchange is over. It reports whether the watch had already closed the stream.
func closeWhenDone(ctx context.Context, stream net.Conn) (release func() (fired bool)) {
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	return func() bool { return !stop() }
}

// handshakeDeadline is when the handshake must be done: the deadline of the caller
// when it has one and it is sooner, and the backstop otherwise.
func handshakeDeadline(ctx context.Context) time.Time {
	backstop := time.Now().Add(dialHandshakeTimeout)
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(backstop) {
		return backstop
	}
	return deadline
}

// remainingBudget is how long the caller is still willing to wait, or zero when
// it did not say. It is zero and not negative for an expired context.
func remainingBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	if remaining := time.Until(deadline); remaining > 0 {
		return remaining
	}
	return 0
}
