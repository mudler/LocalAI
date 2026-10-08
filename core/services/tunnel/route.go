package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/libp2p/go-yamux/v5"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// ErrNoRoute reports that this replica could not get a request to a worker right
// now. It is a routing fact and nothing more.
//
// It says nothing about whether the worker exists or runs. A worker that is
// registered, heartbeating and serving can be unroutable from here for ordinary
// reasons: it has not dialled its tunnel yet after an upgrade of the frontends,
// the replica that holds its tunnel is restarting, the ownership row is a moment
// old, or this replica has no way to reach the owner.
//
// Every failure to resolve or open a route carries it, so that a consumer that
// must not act on absence has one check to make. The specific cause stays in the
// chain for a caller that can act on it, with the exception that
// routeFailure describes.
//
// It is not carried by:
//   - a path that is slow (ErrTransport), a reply that breaks the protocol
//     (ErrProtocol) or a database that did not answer (ErrInfrastructure);
//   - a refusal that the worker wrote, because a worker that answers is present;
//   - a peer replica that did not answer (ErrPeerUnreachable), because that is a
//     fact about the link between two frontends and a scheduler that demoted a
//     worker for it would take capacity away for no reason of the worker;
//   - the budget of the caller running out, because an expiry says nothing about
//     any worker.
var ErrNoRoute = errors.New("tunnel: no route from this replica to that worker")

// ErrNoRelayPath reports that the tunnel of a worker is held by another replica
// and this one has no way to reach that replica. It is its own condition: not
// ErrNotOwner, which tells a caller to resolve the owner again, and not
// ErrPeerUnreachable, because no peer was dialled.
var ErrNoRelayPath = errors.New("tunnel: this replica cannot relay to the owner of that worker")

// ErrTransport reports that the tunnel path to a worker did not carry a request
// in time: a socket deadline ran out, a keepalive was missed, a worker took the
// request and said nothing. It is a fact about how fast the path is, not about
// whether a route exists. A scheduler that demoted a worker on it would take
// capacity away from a worker that is slow to answer.
var ErrTransport = errors.New("tunnel: the path to that worker did not carry the request in time")

// ErrProtocol reports a reply on a stream that is not part of the protocol of
// the tunnel: a worker or a relay that answered with something else. It is a
// defect of the other end, and not a missing route.
var ErrProtocol = errors.New("tunnel: unexpected reply on a stream")

// ErrInfrastructure reports that the shared database, which holds the owner of
// each tunnel, could not answer. It says nothing about any worker: the worker
// may be well, and so may the replica that holds it.
var ErrInfrastructure = errors.New("tunnel: the cluster database could not answer")

// noRouteError reports a worker that this replica cannot route to, keeping the
// cause in its message and out of its unwrap chain.
//
// The causes are absence claims: no live replica holds the tunnel of the worker,
// and no such replica is registered. Both are true statements about the cluster.
// Neither is a statement about the worker, and a consumer that matched on them
// would conclude that a worker that is heartbeating and serving has gone away and
// reclaim its models. The guarantee belongs to the type: no call site can leak an
// absence sentinel through it.
type noRouteError struct {
	nodeID string
	cause  error
}

func (e *noRouteError) Error() string {
	return fmt.Sprintf("tunnel: no route from this replica to node %q: %v", e.nodeID, e.cause)
}

// Unwrap reports only ErrNoRoute.
func (e *noRouteError) Unwrap() error { return ErrNoRoute }

// isAbsenceClaim reports whether an error asserts that something does not exist.
func isAbsenceClaim(err error) bool {
	return errors.Is(err, cluster.ErrNoConnection) || errors.Is(err, cluster.ErrInstanceNotFound)
}

// routeFailure turns the failure of a dial into the error that the caller gets.
// It is the one place that decides, so that two predicates cannot drift apart.
//
//   - An absence claim does not reach the caller. It becomes ErrNoRoute with the
//     cause in the message only.
//   - A failure of the budget of the caller, of the link to a peer replica, of the
//     pool, of the bulk lane alone, of the database, or a reply that breaks the
//     protocol is returned as itself, wrapped, with no ErrNoRoute. None of them is
//     a statement about whether the worker has a route, and ErrNoRoute makes a
//     scheduler demote the worker.
//   - A timeout of a socket or of the multiplexer is ErrTransport. A path that is
//     slow is not a path that does not exist.
//   - ErrNoRoute is for the routing facts: nothing holds the tunnel of the node,
//     this replica cannot reach the one that does, the owner could not open a
//     stream, the worker could not serve one, or the session ended under the
//     request. The cause stays below it.
//   - Anything else is returned as a plain wrapped error. A failure that nobody
//     classified must not demote a worker.
func routeFailure(nodeID string, cause error) error {
	switch {
	case isAbsenceClaim(cause):
		return &noRouteError{nodeID: nodeID, cause: cause}
	case errors.Is(cause, context.DeadlineExceeded), errors.Is(cause, context.Canceled),
		errors.Is(cause, ErrPeerUnreachable), errors.Is(cause, ErrPoolClosed),
		errors.Is(cause, ErrNoBulkSession), errors.Is(cause, ErrInfrastructure),
		errors.Is(cause, ErrProtocol), errors.Is(cause, ErrTransport):
		return fmt.Errorf("reaching node %q: %w", nodeID, cause)
	case isTimeout(cause):
		return fmt.Errorf("reaching node %q: %w: %w", nodeID, ErrTransport, cause)
	case isRoutingFact(cause):
		return fmt.Errorf("reaching node %q: %w: %w", nodeID, ErrNoRoute, cause)
	}
	return fmt.Errorf("reaching node %q: %w", nodeID, cause)
}

// isTimeout reports whether a deadline of a socket or of the multiplexer ended
// the attempt.
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, yamux.ErrTimeout) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// isRoutingFact reports whether err says that the path to the worker is not
// there: not held, not reachable through a relay, or ended under the request.
func isRoutingFact(err error) bool {
	for _, fact := range []error{
		ErrNotOwner, ErrNoRelayPath, ErrRelayUnavailable, ErrRelayRequestInvalid,
		ErrStreamNotServed,
		yamux.ErrSessionShutdown, yamux.ErrStreamClosed, yamux.ErrStreamReset, yamux.ErrRemoteGoAway,
		io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, net.ErrClosed,
	} {
		if errors.Is(err, fact) {
			return true
		}
	}
	return false
}
