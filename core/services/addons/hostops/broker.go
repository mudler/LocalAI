// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	"github.com/mudler/xlog"
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
	// ErrForbidden is the refusal of an inbound operation: the add-on has no
	// right to what it named.
	ErrForbidden = errors.New("forbidden")
)

// Call is what a handler receives. The actor is the one of the live request,
// never a value from the arguments.
type Call struct {
	AddonID, OperationID string
	// ResourceID is the resource of the live request, as the host put it in
	// the envelope (for an action tool, the key of the chat). It is never a
	// value from the arguments.
	ResourceID string
	// Scope is the scope of the live request's authority.
	Scope string
	// ChatID and ChatPrincipal name the chat the live request was made for,
	// from the host's own record of it (InFlight), never from the envelope
	// or the arguments. Both are empty for a request no chat made.
	ChatID, ChatPrincipal string
	Actor                 contract.Actor
	Arguments             json.RawMessage
}

type Handler func(ctx context.Context, call Call) (json.RawMessage, error)

// ScopeInbound is the authorization class of an operation that an add-on starts
// on its own, with no user behind it: a message that arrives from outside. It
// is authorized by the credential of the live incarnation alone, so the call has
// no actor and no feature check, and the handler must find its authority in the
// host's own records (for a connector, the stored binding), never in the
// arguments.
const ScopeInbound = "inbound"

// Spec declares one host operation. Scope is "read", "apply", "act" or
// ScopeInbound. Feature is the permission the actor must hold; it is required
// except for ScopeInbound, which has no actor. Admin marks an operation only an
// administrator may run, on top of the feature.
type Spec struct {
	Op, Scope, Feature string
	Idempotent         bool
	Admin              bool
	// Timeout replaces the default for the scope when it is set.
	Timeout time.Duration
	Handler Handler
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
	if s.Scope != "read" && s.Scope != "apply" && s.Scope != "act" && s.Scope != ScopeInbound {
		return fmt.Errorf("host operation %q: scope must be read, apply, act or %s", s.Op, ScopeInbound)
	}
	if s.Handler == nil || (s.Feature == "" && s.Scope != ScopeInbound) {
		return fmt.Errorf("host operation %q: feature and handler are required", s.Op)
	}
	if s.Scope == ScopeInbound && (s.Feature != "" || s.Admin) {
		return fmt.Errorf("host operation %q: an inbound operation has no actor, so no feature or admin check", s.Op)
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
// It is a fixed list: use HandlerForRuntime with an Authority for grants that
// change while the add-on runs.
func (b *Broker) Handler(addonID, credential string, allowed []string) http.Handler {
	return b.HandlerForRuntime(addonID, credential, NewStaticAuthority(map[string][]string{addonID: allowed}), 0, "")
}

// RuntimeEndpoint owns callback admission and draining for one runtime. Stop
// prevents new HTTP requests and handler admissions. Drain waits for admitted
// requests, including those still decoding, without holding a lock across them.
// Retired observation correlations must not be forgotten until Drain returns.
type RuntimeEndpoint struct {
	handler http.Handler
	mu      sync.Mutex
	closed  bool
	active  sync.WaitGroup
}

type endpointKey struct{}

// EndpointForRuntime serves one runtime. auth is asked on every call, so a
// revoke or a deny applies to the next call; a nil auth refuses every call.
func (b *Broker) EndpointForRuntime(addonID, credential string, auth Authority, generation uint64, incarnation string) *RuntimeEndpoint {
	return &RuntimeEndpoint{handler: b.HandlerForRuntime(addonID, credential, auth, generation, incarnation)}
}

func (e *RuntimeEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		refuse(w, http.StatusForbidden, contract.HostNoLiveOperation, "runtime endpoint closed")
		return
	}
	e.active.Add(1)
	e.mu.Unlock()
	defer e.active.Done()
	e.handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), endpointKey{}, e)))
}

func (e *RuntimeEndpoint) Stop() { e.mu.Lock(); e.closed = true; e.mu.Unlock() }

// Drain must be called after Stop, which serializes admission with Wait.
func (e *RuntimeEndpoint) Drain() { e.active.Wait() }

// admit linearizes handler admission against Stop. No mutex is held while
// running a handler: cancellation and teardown may themselves need the broker.
func (e *RuntimeEndpoint) admit() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.closed
}

// HandlerForRuntime authenticates callbacks for exactly one runtime. The
// authority is asked for every call, after the credential check and before
// anything else of the operation runs. A call that already runs is not
// interrupted by a later revoke.
func (b *Broker) HandlerForRuntime(addonID, credential string, auth Authority, generation uint64, incarnation string) http.Handler {
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
		if b.inflight.callbackDenied(addonID, req.OperationID, generation, incarnation) {
			refuse(w, http.StatusForbidden, contract.HostScope, "observation callbacks denied")
			return
		}
		// The add-on must not learn whether an operation is denied or only not
		// granted, so the body is the same. The reason stays on the host.
		if auth == nil {
			refuse(w, http.StatusForbidden, contract.HostUnknownOp, "operation not allowed")
			return
		}
		if ok, why := auth.Allows(addonID, req.Op); !ok {
			if why.Denied() || why == ReasonIdentityMismatch {
				xlog.Info("add-on host operation refused", "addon", addonID, "op", req.Op, "reason", string(why))
			}
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
		if spec.Scope == ScopeInbound {
			b.serveInbound(w, r, spec, addonID, req)
			return
		}
		env, binding, live := b.inflight.LookupBound(addonID, req.OperationID)
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
		if spec.Timeout > 0 {
			timeout = spec.Timeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		call := Call{AddonID: addonID, OperationID: req.OperationID, ResourceID: env.Request.ResourceID, Scope: env.Authority.Scope,
			ChatID: binding.ChatID, ChatPrincipal: binding.Principal, Actor: actor, Arguments: args}
		result, err := b.run(ctx, spec, call)
		if err != nil {
			status, code, msg := http.StatusServiceUnavailable, contract.HostUnavailable, "operation unavailable"
			switch {
			case errors.Is(err, ErrForbidden):
				status, code, msg = http.StatusForbidden, contract.HostNoLiveOperation, "runtime endpoint closed"
			case errors.Is(err, ErrNotFound):
				status, code, msg = http.StatusNotFound, contract.HostNotFound, "not found"
			case errors.Is(err, ErrConflict):
				status, code, msg = http.StatusConflict, contract.HostConflict, "conflict"
			case errors.Is(err, modelsprobe.ErrNotShared):
				status, code, msg = http.StatusConflict, contract.HostConflict, modelsprobe.ErrNotShared.Error()
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

// serveInbound runs an inbound operation. The credential of the incarnation was
// checked by the caller, and that credential is the whole authorization: the
// call has no actor. The add-on ID is the one the broker was built for, never a
// value of the request.
func (b *Broker) serveInbound(w http.ResponseWriter, r *http.Request, spec Spec, addonID string, req contract.HostRequest) {
	args, err := checkArguments(req.Arguments)
	if err != nil {
		refuse(w, http.StatusBadRequest, contract.HostBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), b.limits.ApplyTimeout)
	defer cancel()
	result, err := b.run(ctx, spec, Call{AddonID: addonID, OperationID: req.OperationID, Arguments: args})
	if err != nil {
		status, code, msg := http.StatusServiceUnavailable, contract.HostUnavailable, "operation unavailable"
		switch {
		case errors.Is(err, ErrForbidden):
			status, code, msg = http.StatusForbidden, contract.HostScope, "not allowed"
		case errors.Is(err, ErrNotFound):
			status, code, msg = http.StatusNotFound, contract.HostNotFound, "not found"
		case errors.Is(err, ErrTransferTooLarge):
			status, code, msg = http.StatusRequestEntityTooLarge, contract.HostTooLarge, "the file is larger than the limit"
		case errors.Is(err, ErrConflict):
			status, code, msg = http.StatusConflict, contract.HostConflict, "conflict"
		case errors.Is(err, ErrBadRequest):
			status, code, msg = http.StatusBadRequest, contract.HostBadRequest, err.Error()
		}
		refuse(w, status, code, msg)
		return
	}
	reply(w, http.StatusOK, contract.HostResponse{Result: result})
}

func hasPrefix(allowed []string, op string) bool {
	for _, p := range allowed {
		if p != "" && strings.HasPrefix(op, p) {
			return true
		}
	}
	return false
}

// scopeAllows: an apply operation needs an apply envelope and an act
// operation needs an act envelope; a read operation runs under a read,
// preview, apply or act envelope.
func scopeAllows(have, need string) bool {
	switch need {
	case "apply":
		return have == "apply"
	case "act":
		return have == "act"
	}
	return have == "read" || have == "preview" || have == "apply" || have == "act"
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
	if endpoint, ok := ctx.Value(endpointKey{}).(*RuntimeEndpoint); ok && !endpoint.admit() {
		return nil, ErrForbidden
	}
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
