package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
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

// natsLink carries control requests as NATS requests to the per-node subject of
// the verb. It maps the failure of the carrier onto the conditions this package
// acts on, and it keeps the carrier's own sentinel out of the unwrap chain on
// purpose: that sentinel names an absence, and a consumer that matched on it
// would read absence as a fact about the node.
type natsLink struct {
	bus messaging.MessagingClient
}

func (n *natsLink) label() string { return "NATS" }

func (n *natsLink) describe(nodeID, verb string) string {
	return "subject=" + natsSubject(nodeID, verb)
}

// natsSubject is the per-node subject of a control verb. A verb that has no
// subject is a programming error; the empty subject then fails the request.
func natsSubject(nodeID, verb string) string {
	switch verb {
	case workerctl.VerbBackendInstall:
		return messaging.SubjectNodeBackendInstall(nodeID)
	case workerctl.VerbBackendUpgrade:
		return messaging.SubjectNodeBackendUpgrade(nodeID)
	case workerctl.VerbBackendStop:
		return messaging.SubjectNodeBackendStop(nodeID)
	case workerctl.VerbBackendDelete:
		return messaging.SubjectNodeBackendDelete(nodeID)
	case workerctl.VerbBackendList:
		return messaging.SubjectNodeBackendList(nodeID)
	case workerctl.VerbModelsRunning:
		return messaging.SubjectNodeModelsRunning(nodeID)
	case workerctl.VerbModelUnload:
		return messaging.SubjectNodeModelUnload(nodeID)
	case workerctl.VerbModelStop:
		return messaging.SubjectNodeModelStop(nodeID)
	case workerctl.VerbModelOp:
		return messaging.SubjectNodeModelOp(nodeID)
	case workerctl.VerbModelDelete:
		return messaging.SubjectNodeModelDelete(nodeID)
	case workerctl.VerbFilesEnsure:
		return messaging.SubjectNodeFilesEnsure(nodeID)
	case workerctl.VerbFilesStage:
		return messaging.SubjectNodeFilesStage(nodeID)
	case workerctl.VerbFilesTemp:
		return messaging.SubjectNodeFilesTemp(nodeID)
	case workerctl.VerbFilesListDir:
		return messaging.SubjectNodeFilesListDir(nodeID)
	case workerctl.VerbFilesRelease:
		return messaging.SubjectNodeFilesRelease(nodeID)
	}
	return ""
}

// request sends the request on the subject of the verb. The context is not
// used: a NATS request has its own timeout.
func (n *natsLink) request(_ context.Context, nodeID, verb string, req, reply any, timeout time.Duration) error {
	subject := natsSubject(nodeID, verb)
	if subject == "" {
		return fmt.Errorf("no NATS subject for control verb %q", verb)
	}
	return controlRequest(n.bus, subject, req, reply, timeout)
}

// controlRequest is messaging.RequestJSON with the failure of the carrier mapped
// onto the conditions this package acts on: a subject that nobody answers is
// ErrNoRoute.
func controlRequest(bus messaging.MessagingClient, subject string, req, reply any, timeout time.Duration) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	replyData, err := bus.Request(subject, data, timeout)
	if err != nil {
		err = fmt.Errorf("NATS request to %s: %w", subject, err)
		if errors.Is(err, nats.ErrNoResponders) {
			return fmt.Errorf("%w: %v", ErrNoRoute, err)
		}
		return err
	}
	if err := json.Unmarshal(replyData, reply); err != nil {
		return fmt.Errorf("unmarshaling reply from %s: %w", subject, err)
	}
	return nil
}

// controlRequestJSON is controlRequest with the types of the request and the
// reply. The carrier's own sentinel is kept out of the unwrap chain on purpose:
// it names an absence, and a consumer that matched on it would read absence as a
// fact about the node.
func controlRequestJSON[Req, Reply any](bus messaging.MessagingClient, subject string, req Req, timeout time.Duration) (*Reply, error) {
	var reply Reply
	if err := controlRequest(bus, subject, req, &reply, timeout); err != nil {
		return nil, err
	}
	return &reply, nil
}

// requestWithProgress subscribes to the per-operation progress subject before
// it sends the request, so that no early event is missed, and drops the
// subscription when the request is over. No subscription is made when onProgress
// is nil or opID is empty: the reconciler-driven retry path and callers that
// predate progress stay silent at no cost.
func (n *natsLink) requestWithProgress(ctx context.Context, nodeID, verb, opID string, req, reply any, timeout time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) error {
	sub := n.subscribeProgress(nodeID, opID, onProgress)
	err := n.request(ctx, nodeID, verb, req, reply, timeout)
	if sub != nil {
		if unsubscribeErr := sub.Unsubscribe(); unsubscribeErr != nil {
			xlog.Warn("Failed to unsubscribe from backend progress", "nodeID", nodeID, "verb", verb, "opID", opID, "error", unsubscribeErr)
		}
	}
	return err
}

// subscribeProgress subscribes to the per-op backend-install progress subject
// so the master can stream per-node download ticks while a worker installs or
// upgrades. Returns nil (and subscribes to nothing) when onProgress is nil or
// opID is empty — the reconciler-driven retry path and legacy callers stay
// silent at no cost. Shared by InstallBackend, UpgradeBackend, and the legacy
// force-install fallback: an upgrade is a force-reinstall, so it reuses the
// install-progress subject rather than minting a new one (no new NATS
// permission, no new rolling-update compat surface). Caller must Unsubscribe
// the returned subscription after the request completes.
func (n *natsLink) subscribeProgress(nodeID, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) messaging.Subscription {
	if onProgress == nil || opID == "" {
		return nil
	}
	progressSubject := messaging.SubjectNodeBackendInstallProgress(nodeID, opID)
	s, subErr := n.bus.Subscribe(progressSubject, func(raw []byte) {
		var ev workerctl.BackendInstallProgressEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			xlog.Debug("malformed backend progress event", "subject", progressSubject, "error", err)
			return
		}
		// Goroutine guard: a slow onProgress callback must not stall the NATS
		// reader thread. Events spawn one goroutine each, so ordering at the
		// consumer is best-effort; the worker debounces to ~250ms which dwarfs
		// goroutine scheduling jitter, and its final Flush() is the terminal tick.
		go onProgress(ev)
	})
	if subErr != nil {
		xlog.Warn("Failed to subscribe to backend progress subject; proceeding without progress streaming",
			"subject", progressSubject, "error", subErr)
		return nil
	}
	return s
}

func (n *natsLink) waitExpired(err error) bool { return isNATSTimeout(err) }

func (n *natsLink) acknowledgementMissing(err error) bool { return isStrictRequestTimeout(err) }

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
