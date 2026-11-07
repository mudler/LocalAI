// SPDX-License-Identifier: MIT

// Package writeflow is the part of the runtime of a Google add-on that does
// not depend on the service: the clock, the journal, the wrapper that marks a
// result as untrusted, the dispatch of a read tool, the translation of an
// error of the API and the state machine that applies a change once.
package writeflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/argutil"
	"github.com/mudler/LocalAI/addons/internal/googleapi"
	"github.com/mudler/LocalAI/addons/internal/journal"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Config is what an add-on says about itself.
type Config struct {
	// Source names the service in a result, for example "gmail".
	Source string
	// Notice is carried by every read result.
	Notice string
	// MaxPayload bounds the JSON of one read result.
	MaxPayload int
	// StaleNote is the answer when the target changed after the preview.
	StaleNote string
	// UnreadableNote is the answer when a change went through and the answer
	// of the service could not be read.
	UnreadableNote string
	// Describe words a refusal of the service for the model.
	Describe func(error) string
	// Refusals are errors only this service gives, which are a refusal and
	// not an outage.
	Refusals []error
	// TranslateWrite is Translate for a failure of a write, where sent says
	// whether the request left.
	TranslateWrite func(err error, sent bool) error
}

// Runtime holds what the Google add-ons share. An add-on embeds it.
type Runtime struct {
	// Now is the clock; a test fixes it.
	Now func() time.Time

	cfg     Config
	applyMu sync.Mutex
	jmu     sync.Mutex
	jrnl    *journal.Journal
	dir     string
}

// New returns a Runtime for the add-on that cfg describes.
func New(cfg Config) *Runtime { return &Runtime{cfg: cfg} }

// SetDataDir keeps the journal of changes in dir, so a restart still knows
// which operations were made.
func (r *Runtime) SetDataDir(dir string) { r.dir = dir }

// Journal returns the journal, opened on first use.
func (r *Runtime) Journal() *journal.Journal {
	r.jmu.Lock()
	defer r.jmu.Unlock()
	if r.jrnl == nil {
		r.jrnl = journal.Open(r.dir)
	}
	return r.jrnl
}

// Time is the current time of the runtime.
func (r *Runtime) Time() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

type wrapped struct {
	Source    string `json:"source"`
	Untrusted bool   `json:"untrusted"`
	Notice    string `json:"notice"`
	Data      any    `json:"data"`
}

// Encode wraps data as untrusted.
func (r *Runtime) Encode(data any) ([]byte, error) {
	return json.Marshal(wrapped{Source: r.cfg.Source, Untrusted: true, Notice: r.cfg.Notice, Data: data})
}

// Finish wraps data as untrusted and checks the size.
func (r *Runtime) Finish(data any) (contract.Result, error) {
	b, err := r.Encode(data)
	if err != nil {
		return contract.Result{}, err
	}
	if len(b) > r.cfg.MaxPayload {
		return contract.Result{}, argutil.Bad("the result is too large")
	}
	return contract.Result{Payload: b}, nil
}

// Small wraps what a receipt echoes. The host copies only a short result into
// a chat, so it holds IDs and links and no text of the service.
func (r *Runtime) Small(data map[string]any) json.RawMessage {
	return argutil.PlainJSON(map[string]any{"source": r.cfg.Source, "untrusted": true, "data": data})
}

// Committed is the receipt of a change that was made.
func (r *Runtime) Committed(op string, rev contract.Revision, out map[string]any) contract.Receipt {
	return contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: rev, Result: contract.Result{Payload: r.Small(out)}}
}

// Read serves a read tool: serve gets the name of the tool and its arguments
// and returns the data, which is wrapped. An error of the API is translated.
func (r *Runtime) Read(env contract.Envelope, serve func(tool string, args map[string]json.RawMessage) (any, error)) (contract.Result, error) {
	tool, raw, err := argutil.SplitArgs(env.Request.Arguments)
	if err != nil {
		return contract.Result{}, err
	}
	if env.Request.Credential == "" {
		return contract.Result{}, argutil.Bad("no access token for this call")
	}
	data, err := serve(tool, raw)
	if err != nil {
		return contract.Result{}, r.Translate(err)
	}
	return r.Finish(data)
}

// Translate maps an error of the API to what the protocol sends. A rejected
// token keeps its own code so the host can renew it; a refusal keeps a fixed
// sentence; a limit or an outage keeps its wait.
func (r *Runtime) Translate(err error) error {
	var limited *contract.ErrRateLimited
	var server *googleapi.ServerError
	switch {
	case errors.Is(err, googleapi.ErrUnauthorized):
		return contract.ErrCredentialRejected
	case errors.As(err, &limited):
		return limited
	case errors.As(err, &server):
		return contract.RateLimitedAfter(server.RetryAfter)
	case errors.Is(err, protocol.ErrBadRequest), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	for _, refusal := range append([]error{googleapi.ErrScope, googleapi.ErrForbidden, googleapi.ErrNotFound, googleapi.ErrInvalid, googleapi.ErrTooLarge}, r.cfg.Refusals...) {
		if errors.Is(err, refusal) {
			return argutil.Bad("%s", r.cfg.Describe(err))
		}
	}
	return err
}

// Receipt answers from the journal. An operation that was started and not
// finished is unknown: the add-on cannot tell whether the service got it.
func (r *Runtime) Receipt(_ context.Context, op string) (contract.Receipt, error) {
	if rc, ok := r.Journal().Receipt(op); ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: op, State: contract.ReceiptUnknown}, nil
}

// definite reports an error that proves the change was not made.
func definite(err error) bool {
	var limited *contract.ErrRateLimited
	return errors.As(err, &limited) || errors.Is(err, googleapi.ErrUnauthorized) || errors.Is(err, googleapi.ErrForbidden) ||
		errors.Is(err, googleapi.ErrScope) || errors.Is(err, googleapi.ErrNotFound) || errors.Is(err, googleapi.ErrInvalid)
}

// Session is one change that was checked and is ready to be applied.
type Session interface {
	// Inspect reads the target as it is now and returns its data revision.
	Inspect() (revision uint64, err error)
	// InspectFailed is asked when Inspect failed, with whether an earlier
	// attempt of the operation started. It may settle the operation from what
	// the failure shows.
	InspectFailed(started bool, err error) (out map[string]any, settled bool)
	// Resume is asked when the revision is current. It may settle the
	// operation from the trace of an earlier attempt, or refuse to go on.
	Resume(started bool) (out map[string]any, settled bool, err error)
	// Send makes the request.
	Send() (out map[string]any, err error)
}

// NoRecovery gives a Session the default for a service that cannot tell
// whether an earlier attempt went through.
type NoRecovery struct{}

// InspectFailed settles nothing.
func (NoRecovery) InspectFailed(bool, error) (map[string]any, bool) { return nil, false }

// Resume settles nothing and lets the attempt go on.
func (NoRecovery) Resume(bool) (map[string]any, bool, error) { return nil, false, nil }

// Partial is the error of a Send that did part of the change and then failed
// for a reason that proves the rest was not made. Out says what was done and
// what was refused, in words for the person. Apply stores it as the receipt
// of the operation, since the first part cannot be taken back by retrying.
type Partial struct {
	Out map[string]any
	Err error
}

func (p *Partial) Error() string { return "part of the change was made: " + p.Err.Error() }
func (p *Partial) Unwrap() error { return p.Err }

// Apply makes the change once. The journal notes an attempt before the
// request leaves and a receipt after it, and the host asks for the receipt of
// an attempt whose outcome is not known. prepare checks the request and
// returns the session.
func (r *Runtime) Apply(op string, rev contract.Revision, prepare func() (Session, error)) (contract.Receipt, error) {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	j := r.Journal()
	if rc, ok := j.Receipt(op); ok {
		return rc, nil
	}
	s, err := prepare()
	if err != nil {
		return contract.Receipt{}, err
	}
	_, started := j.Started(op)
	now, err := s.Inspect()
	if err != nil {
		if out, settled := s.InspectFailed(started, err); settled {
			return r.commit(j, op, rev, out)
		}
		// Nothing was sent yet, so a limit is a refusal here, not an unknown.
		return contract.Receipt{}, r.cfg.TranslateWrite(err, false)
	}
	if now != rev.Data {
		rc := contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Revision: rev,
			Result: contract.Result{Payload: r.Small(map[string]any{"stale": true, "note": r.cfg.StaleNote})}}
		return rc, j.Commit(rc)
	}
	out, settled, err := s.Resume(started)
	if err != nil {
		return contract.Receipt{}, err
	}
	if settled {
		return r.commit(j, op, rev, out)
	}
	if err := j.Begin(op, r.Time()); err != nil {
		return contract.Receipt{}, err
	}
	out, err = s.Send()
	var partial *Partial
	switch {
	case err == nil:
	case errors.As(err, &partial) && definite(partial.Err):
		// The first part is done and the rest is known not to be. Keep a
		// receipt that says so; a retry then asks for the rest only.
		out = partial.Out
	case errors.Is(err, googleapi.ErrUnreadable):
		out = map[string]any{"note": r.cfg.UnreadableNote}
	case definite(err):
		j.Forget(op)
		return contract.Receipt{}, r.cfg.TranslateWrite(err, true)
	default:
		// The request may or may not have arrived. The host will ask for the
		// receipt, get "unknown", and leave the decision to the person.
		return contract.Receipt{}, err
	}
	return r.commit(j, op, rev, out)
}

func (r *Runtime) commit(j *journal.Journal, op string, rev contract.Revision, out map[string]any) (contract.Receipt, error) {
	rc := r.Committed(op, rev, out)
	return rc, j.Commit(rc)
}
