// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Errors a Handler returns to say what went wrong. Any other error is an
// outage (503), never a refusal.
var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrBadRequest = errors.New("invalid request")
)

// Call is what a handler receives. The actor is the one of the live request,
// never a value from the arguments.
type Call struct {
	AddonID, OperationID string
	Actor                contract.Actor
	Arguments            json.RawMessage
}

type Handler func(ctx context.Context, call Call) (json.RawMessage, error)

// Spec declares one host operation. Scope is "read" or "apply". Feature is
// the permission the actor must hold; it is required. Admin marks an operation
// only an administrator may run, on top of the feature.
type Spec struct {
	Op, Scope, Feature string
	Idempotent         bool
	Admin              bool
	Handler            Handler
}

const (
	idempotentEntries = 256
	idempotentTTL     = time.Hour
)

// Broker holds the registered operations. It serves none until one is
// registered, and an add-on gets only the operations its prefix list allows.
type Broker struct {
	inflight *InFlight
	has      func(userID, feature string) bool
	admin    func(actor contract.Actor) bool
	limits   contract.Limits
	now      func() time.Time

	mu    sync.Mutex
	specs map[string]Spec
	done  map[string]*cached
	// datasets is set by RegisterEngineDatasets and read by finetune.start.
	datasets *datasetIndex
}

type cached struct {
	ready  chan struct{}
	result json.RawMessage
	err    error
	at     time.Time
}

// New builds a broker. has answers whether a user holds a feature; a nil has
// refuses every operation.
func New(inflight *InFlight, has func(userID, feature string) bool, limits contract.Limits) *Broker {
	return &Broker{inflight: inflight, has: has, limits: limits, now: time.Now, specs: map[string]Spec{}, done: map[string]*cached{}}
}

// SetAdminCheck sets the answer to whether an actor is an administrator. It is
// the check of the operations marked Admin, and it decides in every mode: the
// broker does not shortcut the local owner. Without it every such operation is
// refused. It is set once, before the broker serves.
func (b *Broker) SetAdminCheck(admin func(actor contract.Actor) bool) { b.admin = admin }

// Register adds an operation. It refuses a duplicate, a malformed name, an
// unknown scope, a missing feature and a missing handler.
func (b *Broker) Register(s Spec) error {
	if !contract.ValidHostOp(s.Op) {
		return fmt.Errorf("host operation %q: malformed name", s.Op)
	}
	if s.Scope != "read" && s.Scope != "apply" {
		return fmt.Errorf("host operation %q: scope must be read or apply", s.Op)
	}
	if s.Feature == "" || s.Handler == nil {
		return fmt.Errorf("host operation %q: feature and handler are required", s.Op)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, dup := b.specs[s.Op]; dup {
		return fmt.Errorf("host operation %q: already registered", s.Op)
	}
	b.specs[s.Op] = s
	return nil
}

// Ops lists the registered operation names.
func (b *Broker) Ops() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.specs))
	for op := range b.specs {
		out = append(out, op)
	}
	return out
}

func reply(w http.ResponseWriter, status int, r contract.HostResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(r)
}

func refuse(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, contract.HostResponse{Error: &contract.HostError{Code: code, Message: msg}})
}

// Handler serves the channel of one add-on incarnation. credential is the
// secret that incarnation holds; allowed lists the operation prefixes the
// host setting gives the add-on. Whatever the check, the default is refuse.
func (b *Broker) Handler(addonID, credential string, allowed []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			refuse(w, http.StatusMethodNotAllowed, contract.HostBadRequest, "POST only")
			return
		}
		if credential == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(contract.HostCredentialHeader)), []byte(credential)) != 1 {
			refuse(w, http.StatusUnauthorized, contract.HostNoLiveOperation, "credential refused")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, b.limits.PayloadBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				refuse(w, http.StatusRequestEntityTooLarge, contract.HostTooLarge, "request too large")
				return
			}
			refuse(w, http.StatusBadRequest, contract.HostBadRequest, "unreadable request")
			return
		}
		req, err := contract.DecodeHostRequest(body, b.limits.PayloadBytes)
		if err != nil || req.OperationID == "" || !contract.ValidHostOp(req.Op) {
			refuse(w, http.StatusBadRequest, contract.HostBadRequest, "malformed request")
			return
		}
		if !hasPrefix(allowed, req.Op) {
			refuse(w, http.StatusForbidden, contract.HostUnknownOp, "operation not allowed")
			return
		}
		b.mu.Lock()
		spec, ok := b.specs[req.Op]
		b.mu.Unlock()
		if !ok {
			refuse(w, http.StatusForbidden, contract.HostUnknownOp, "operation not allowed")
			return
		}
		env, live := b.inflight.Lookup(addonID, req.OperationID)
		if !live {
			refuse(w, http.StatusForbidden, contract.HostNoLiveOperation, "no live request")
			return
		}
		if !scopeAllows(env.Authority.Scope, spec.Scope) {
			refuse(w, http.StatusForbidden, contract.HostScope, "scope too low")
			return
		}
		args, err := checkArguments(req.Arguments)
		if err != nil {
			refuse(w, http.StatusBadRequest, contract.HostBadRequest, err.Error())
			return
		}
		actor := env.Authority.Actor
		if b.has == nil || (actor.Mode != "local-owner" && !b.has(actor.UserID, spec.Feature)) {
			refuse(w, http.StatusForbidden, contract.HostFeature, "feature not granted")
			return
		}
		if spec.Admin && (b.admin == nil || !b.admin(actor)) {
			refuse(w, http.StatusForbidden, contract.HostFeature, "administrator required")
			return
		}
		timeout := b.limits.ReadTimeout
		if spec.Scope == "apply" {
			timeout = b.limits.ApplyTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		call := Call{AddonID: addonID, OperationID: req.OperationID, Actor: actor, Arguments: args}
		result, err := b.run(ctx, spec, call)
		if err != nil {
			status, code, msg := http.StatusServiceUnavailable, contract.HostUnavailable, "operation unavailable"
			switch {
			case errors.Is(err, ErrNotFound):
				status, code, msg = http.StatusNotFound, contract.HostNotFound, "not found"
			case errors.Is(err, ErrConflict):
				status, code, msg = http.StatusConflict, contract.HostConflict, "conflict"
			case errors.Is(err, ErrBadRequest):
				// The message names the refused field. Handlers build it from
				// fixed text and field names, never from a server path.
				status, code, msg = http.StatusBadRequest, contract.HostBadRequest, err.Error()
			}
			refuse(w, status, code, msg)
			return
		}
		reply(w, http.StatusOK, contract.HostResponse{Result: result})
	})
}

func hasPrefix(allowed []string, op string) bool {
	for _, p := range allowed {
		if p != "" && strings.HasPrefix(op, p) {
			return true
		}
	}
	return false
}

// scopeAllows: an apply operation needs an apply envelope; a read operation
// runs under a read, preview or apply envelope.
func scopeAllows(have, need string) bool {
	if need == "apply" {
		return have == "apply"
	}
	return have == "read" || have == "preview" || have == "apply"
}

// checkArguments returns the argument object, `{}` when absent. A name of
// identity or authority is refused whatever a schema says.
func checkArguments(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`), nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, errors.New("arguments must be an object")
	}
	for name := range obj {
		if contract.ForbiddenToolProperty(name) {
			return nil, errors.New("argument names an identity or authority")
		}
	}
	return raw, nil
}

// run executes the handler. An idempotent operation runs once per add-on,
// operation ID and name; a repeat gets the first result, and a call that
// arrives while the first still runs waits for it. A failure is not kept.
func (b *Broker) run(ctx context.Context, spec Spec, call Call) (json.RawMessage, error) {
	if !spec.Idempotent {
		return spec.Handler(ctx, call)
	}
	k := call.AddonID + "/" + call.OperationID + "/" + spec.Op
	b.mu.Lock()
	b.sweepLocked()
	if c := b.done[k]; c != nil {
		b.mu.Unlock()
		select {
		case <-c.ready:
			return c.result, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(b.done) >= idempotentEntries {
		b.mu.Unlock()
		return nil, errors.New("idempotent cache full")
	}
	c := &cached{ready: make(chan struct{}), at: b.now()}
	b.done[k] = c
	b.mu.Unlock()
	c.result, c.err = spec.Handler(ctx, call)
	if c.err != nil {
		b.mu.Lock()
		delete(b.done, k)
		b.mu.Unlock()
	}
	close(c.ready)
	return c.result, c.err
}

func (b *Broker) sweepLocked() {
	cut := b.now().Add(-idempotentTTL)
	for k, c := range b.done {
		select {
		case <-c.ready:
			if c.at.Before(cut) {
				delete(b.done, k)
			}
		default:
		}
	}
}
