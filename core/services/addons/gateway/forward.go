// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The calls a frontend forwards to the owner (OAuth, host grants, library)
// share one shape: check that the owner supports the call, send it, read a
// bounded reply envelope, and report a lost answer as an unknown outcome when
// the call changes something. forward and writeReply hold that shape once.

// replyEnvelope is a reply of the owner: data or a refusal the owner decided.
type replyEnvelope[T any] interface {
	// open returns the data, whether the envelope is well formed, and the
	// refusal the owner decided, if any.
	open() (data T, valid bool, refusal error)
}

// ownerCall says how one call is forwarded.
type ownerCall struct {
	// need is the capability the owner must announce, with reason as the
	// text of the unavailable answer when it does not. Empty needs none.
	need, reason string
	// id is the operation ID; empty makes a new one.
	id     string
	action string
	// limit bounds the reply, in bytes.
	limit int64
}

// forward sends wr to the owner and returns the data of its reply. A refusal
// the owner decided is returned as its own error type. wr carries the call;
// forward sets its request.
func forward[T, R any, PR interface {
	*R
	replyEnvelope[T]
}](ctx context.Context, c *Carrier, a contract.Actor, oc ownerCall, wr wireRequest) (T, error) {
	var zero T
	if oc.need != "" {
		caps, e := c.Capabilities(ctx)
		if e != nil {
			return zero, e
		}
		if !caps.Supports(oc.need) {
			return zero, &UnavailableError{Reason: oc.reason}
		}
	}
	id := oc.id
	if id == "" {
		var e error
		if id, e = newResourceOperationID(); e != nil {
			return zero, ErrUnavailable
		}
	}
	wr.Request = contract.Request{OperationID: id, Action: oc.action}
	resp, e := c.send(ctx, a, wr)
	if e != nil {
		return zero, e
	}
	defer func() { _ = resp.Body.Close() }()
	var reply R
	raw, ok := readBounded(resp.Body, oc.limit)
	if !ok || decode(bytes.NewReader(raw), &reply) != nil {
		return zero, lostAnswer(oc.action, id)
	}
	data, valid, refusal := PR(&reply).open()
	switch {
	case !valid:
		return zero, lostAnswer(oc.action, id)
	case refusal != nil:
		return zero, refusal
	}
	return data, nil
}

// lostAnswer is the error for a reply that did not arrive or cannot be read.
// The owner can fail after it applied the work, so a change is an unknown
// outcome and a read is plainly unavailable.
func lostAnswer(action, id string) error {
	if mutation(action) {
		return &OutcomeUnknown{OperationID: id}
	}
	return ErrUnavailable
}

// readBounded reads at most limit bytes. It reports false when the body is
// longer or cannot be read.
func readBounded(r io.Reader, limit int64) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	return raw, err == nil && int64(len(raw)) <= limit
}

// writeReply writes the reply of a call that ran on the owner. A refusal the
// owner decided goes into the envelope with status 200, so every frontend
// gives the same answer; any other error becomes a typed error response.
// success is the envelope of the data and refusal turns a decided error into
// its envelope.
func writeReply(w http.ResponseWriter, err error, success any, refusal func(error) (any, bool)) {
	if err == nil {
		writeEnvelope(w, success)
		return
	}
	if env, ok := refusal(err); ok {
		writeEnvelope(w, env)
		return
	}
	typedError(w, err)
}

func writeEnvelope(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// decided makes the refusal function of writeReply for an error type E that
// the owner decides: errors.As finds it and wrap puts it in the envelope.
func decided[E any, PE interface {
	*E
	error
}](wrap func(PE) any) func(error) (any, bool) {
	return func(err error) (any, bool) {
		var pe PE
		if errors.As(err, &pe) {
			return wrap(pe), true
		}
		return nil, false
	}
}

// readActions are the actions that change nothing, so a lost answer is
// plainly unavailable. mutation fails closed: an action that is not here is
// reported as an unknown outcome until it is classified.
var readActions = map[string]bool{
	ActionHostSettingsRead: true, executionBindingAction: true, executionStatusAction: true,
	"read": true, "operation-status": true, "proposal-get": true, "proposal-reconcile": true, "proposal-list": true,
	"artifact-status": true, appsListAction: true,
}

// markRead classifies actions as reads.
func markRead(actions ...string) {
	for _, a := range actions {
		readActions[a] = true
	}
}
