package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// ErrNoRoute reports that a control command could not be delivered to a node:
// nothing is listening for it right now.
//
// It is a routing fact and says nothing about whether the node exists or is
// serving. The one reaction a consumer may have is the status-only demotion
// (MarkUnhealthy), which the next heartbeat reverses. Nothing may delete a
// node's model rows on it: a node that is registered and heartbeating can be
// unroutable for ordinary reasons, and reclaiming its models would evict healthy
// work.
//
// It is NOT returned for a timeout, for a transport fault, or for a worker that
// answered with a refusal. A node that answers is present by demonstration.
var ErrNoRoute = errors.New("nodes: no route to that node")

// controlRequestJSON is messaging.RequestJSON with the carrier's failure mapped
// onto the conditions this package acts on. The carrier's own sentinel is kept
// out of the unwrap chain on purpose: it names an absence, and a consumer that
// matched on it would read absence as a fact about the node.
func controlRequestJSON[Req, Reply any](bus messaging.MessagingClient, subject string, req Req, timeout time.Duration) (*Reply, error) {
	reply, err := messaging.RequestJSON[Req, Reply](bus, subject, req, timeout)
	if err != nil && errors.Is(err, nats.ErrNoResponders) {
		return nil, fmt.Errorf("%w: %v", ErrNoRoute, err)
	}
	return reply, err
}

// isNATSTimeout returns true if err looks like a NATS request-reply timeout.
// nats.ErrTimeout is the canonical sentinel; context.DeadlineExceeded can
// also surface depending on the client's path; we accept both, plus a
// string-match fallback for clients that return a bare error.
func isNATSTimeout(err error) bool {
	if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "nats: timeout")
}

// isStrictRequestTimeout matches only the carrier's own timeout sentinel.
// stopBackend reads a timeout as "an older worker performed the stop without
// replying" and reports success, so it must not inherit isNATSTimeout's wider
// net: a context deadline or a look-alike message there would turn a real
// failure into a silent success.
func isStrictRequestTimeout(err error) bool {
	return errors.Is(err, nats.ErrTimeout)
}
