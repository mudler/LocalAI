package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// TunnelControl implements NodeControl over the HTTP control plane of a worker.
// It is the second carrier of the verbs that RemoteUnloaderAdapter sends over
// NATS: the same requests, the same timeouts and the same reading of a reply,
// because both are nodeControl. Only the way a request travels differs, and with
// it the way a failure is told apart.
type TunnelControl struct {
	*nodeControl
}

// NewTunnelControl returns the sender that reaches workers through client.
// installTimeout and upgradeTimeout bound the install and upgrade verbs.
func NewTunnelControl(registry ModelLocator, client *ControlClient, installTimeout, upgradeTimeout time.Duration) *TunnelControl {
	return &TunnelControl{nodeControl: &nodeControl{
		registry:       registry,
		link:           &httpLink{client: client},
		installTimeout: installTimeout,
		upgradeTimeout: upgradeTimeout,
	}}
}

// httpLink carries a control request as an HTTP request. A verb that runs for
// minutes streams its progress in the body of the same response, so nothing can
// arrive before the caller listens, and nothing is dropped.
type httpLink struct {
	client *ControlClient
	// trace, when set, is called with the verb and the timeout of every request.
	// The specs of the carrier read what was sent through it.
	trace func(verb string, timeout time.Duration)
	// scale, when it is above zero, shortens every timeout by that factor. The
	// specs of the carrier use it to wait out a silent worker without waiting
	// minutes. trace still reports the timeout of the contract.
	scale float64
}

func (l *httpLink) bound(timeout time.Duration) time.Duration {
	if l.scale > 0 {
		return time.Duration(float64(timeout) * l.scale)
	}
	return timeout
}

func (l *httpLink) label() string { return "tunnel" }

func (l *httpLink) describe(_, verb string) string { return "path=" + workerctl.PathOf(verb) }

func (l *httpLink) request(ctx context.Context, nodeID, verb string, req, reply any, timeout time.Duration) error {
	if l.trace != nil {
		l.trace(verb, timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, l.bound(timeout))
	defer cancel()
	return l.client.Call(ctx, nodeID, verb, req, reply)
}

func (l *httpLink) requestWithProgress(ctx context.Context, nodeID, verb, opID string, req, reply any, timeout time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) error {
	if l.trace != nil {
		l.trace(verb, timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, l.bound(timeout))
	defer cancel()
	var sink func(string, json.RawMessage)
	if onProgress != nil && opID != "" {
		sink = func(_ string, raw json.RawMessage) {
			var ev workerctl.BackendInstallProgressEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				xlog.Debug("malformed backend progress event", "node", nodeID, "error", err)
				return
			}
			onProgress(ev)
		}
	}
	return l.client.CallStreaming(ctx, nodeID, verb, req, reply, sink)
}

// waitExpired is true for the timeout that this link set on the request, and for
// a stream that broke after the worker accepted the verb. In both the work may
// still be running, and the caller must not count the call as a failed attempt
// and fire it again. A reply that cannot be read is neither.
func (l *httpLink) waitExpired(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errStreamBroken)
}

// acknowledgementMissing is always false. An HTTP request has no way to tell a
// worker that stopped the backend and did not reply from one that did not
// receive the request, so silence is a failure here and never a success.
func (l *httpLink) acknowledgementMissing(error) bool { return false }
