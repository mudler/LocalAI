package tunnel

import (
	"context"
	"errors"
	"fmt"

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
//     pool, or of the bulk lane alone is returned as itself, wrapped, with no
//     ErrNoRoute. None of them is a statement about whether the worker has a
//     route, and ErrNoRoute makes a scheduler demote the worker.
//   - Everything else is a route that does not exist now: ErrNoRoute on top, and
//     the cause below it.
func routeFailure(nodeID string, cause error) error {
	switch {
	case isAbsenceClaim(cause):
		return &noRouteError{nodeID: nodeID, cause: cause}
	case errors.Is(cause, context.DeadlineExceeded), errors.Is(cause, context.Canceled),
		errors.Is(cause, ErrPeerUnreachable), errors.Is(cause, ErrPoolClosed),
		errors.Is(cause, ErrNoBulkSession):
		return fmt.Errorf("reaching node %q: %w", nodeID, cause)
	}
	return fmt.Errorf("reaching node %q: %w: %w", nodeID, ErrNoRoute, cause)
}
