// SPDX-License-Identifier: MIT

// Package hostops serves the host operation channel: the calls an add-on
// process makes to ask the host to do what the add-on cannot do itself.
package hostops

import (
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// InFlight holds the envelope of every request the host is serving for an
// add-on right now. A host operation is allowed only while its operation ID
// is in here, so an add-on cannot act when no user asked it to.
type InFlight struct {
	mu           sync.Mutex
	entries      map[string]*live
	observations map[string]*observation
}

type live struct {
	env contract.Envelope
	// binding is the chat the request was made for; bound is false for a
	// request no chat made.
	incarnation string
	observation *observation
	binding     Binding
	bound       bool
	// mixed is set when two requests share the operation ID with different
	// bindings. Neither chat can be trusted, so the entry is not live.
	mixed bool
	n     int
}

func NewInFlight() *InFlight {
	return &InFlight{entries: map[string]*live{}, observations: map[string]*observation{}}
}

func key(addonID, operationID string) string { return addonID + "/" + operationID }

// Begin records the envelope of a request that no chat made. The returned
// function removes it; it is safe to call more than once. Two requests with
// the same operation ID share one entry until both end.
func (f *InFlight) Begin(env contract.Envelope) (end func()) {
	return f.begin(env, Binding{}, false, "")
}

// BeginBound records the envelope of a request made for the chat b. The
// binding stays on the host: the envelope the add-on receives is unchanged.
func (f *InFlight) BeginBound(env contract.Envelope, b Binding) (end func()) {
	return f.begin(env, b, true, "")
}

func (f *InFlight) begin(env contract.Envelope, b Binding, bound bool, incarnation string) (end func()) {
	k := key(env.Request.AddonID, env.Request.OperationID)
	f.mu.Lock()
	if e := f.entries[k]; e != nil {
		e.n++
		if e.observation != nil || e.bound != bound || e.binding != b || e.incarnation != incarnation || e.env.Request.Generation != env.Request.Generation || e.env.Authority.Actor != env.Authority.Actor || e.env.Authority.Scope != env.Authority.Scope || e.env.Request.ResourceID != env.Request.ResourceID {
			e.mixed = true
		}
	} else {
		f.entries[k] = &live{env: env, binding: b, bound: bound, incarnation: incarnation, n: 1}
	}
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if e := f.entries[k]; e != nil {
				if e.n--; e.n <= 0 {
					delete(f.entries, k)
				}
			}
		})
	}
}

// Lookup returns the envelope of a live request. An envelope whose authority
// has expired is not live.
func (f *InFlight) Lookup(addonID, operationID string) (contract.Envelope, bool) {
	env, _, ok := f.LookupBound(addonID, operationID)
	return env, ok
}

// LookupBound returns the envelope of a live request and the chat it was made
// for (the zero Binding for a request no chat made). An expired authority, or
// an operation ID that requests of different chats share, is not live.
func (f *InFlight) LookupBound(addonID, operationID string) (contract.Envelope, Binding, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entries[key(addonID, operationID)]
	if e == nil || e.mixed || !e.env.Authority.ExpiresAt.After(time.Now()) {
		return contract.Envelope{}, Binding{}, false
	}
	return e.env, e.binding, true
}

// observation is host-private, never decoded from an addon message.
type observation struct {
	env                  contract.Envelope
	incarnation, request string
	denied               bool
}

// BeginRuntime binds a live envelope to the authenticated runtime incarnation.
func (f *InFlight) BeginRuntime(env contract.Envelope, b Binding, bound bool, incarnation string) func() {
	return f.begin(env, b, bound, incarnation)
}

// BeginObservation reserves a unique operation for a host-owned observation.
// End removes the live authority idempotently. A bounded retired correlation is
// kept until the runtime endpoint closes, so late inbound-class callbacks cannot
// regain ordinary authority. At capacity admission fails closed (no eviction).
func (f *InFlight) BeginObservation(env contract.Envelope, b Binding, incarnation, request string) (func(), func() bool, error) {
	if incarnation == "" || request == "" || len(request) > 256 || env.Request.OperationID == "" {
		return nil, nil, errors.New("missing observation identity")
	}
	f.mu.Lock()
	k := key(env.Request.AddonID, env.Request.OperationID)
	if len(f.observations) >= 4096 || f.entries[k] != nil || f.observations[k] != nil {
		f.mu.Unlock()
		return nil, nil, errors.New("operation already used")
	}
	o := &observation{env: env, incarnation: incarnation, request: request}
	f.observations[k] = o
	f.entries[k] = &live{env: env, binding: b, bound: b.Valid(), incarnation: incarnation, observation: o, n: 1}
	f.mu.Unlock()
	var once sync.Once
	end := func() { once.Do(func() { f.mu.Lock(); delete(f.entries, k); f.mu.Unlock() }) }
	return end, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return o.denied }, nil
}

func (f *InFlight) callbackDenied(addon, op string, generation uint64, incarnation string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o := f.observations[key(addon, op)]; o != nil {
		o.denied = true
		return true
	}
	if e := f.entries[key(addon, op)]; e != nil && incarnation != "" && (e.incarnation != incarnation || e.env.Request.Generation != generation) {
		return true
	}
	return false
}

// ForgetRuntime releases retired observation correlations only after the callback
// endpoint has stopped admission and all admitted HTTP handlers have drained.
// Server.Close alone is insufficient: it does not wait for active handlers.
// Live entries are removed too; the stopped endpoint refuses subsequent calls.
func (f *InFlight) ForgetRuntime(addon, incarnation string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, o := range f.observations {
		if o.env.Request.AddonID == addon && o.incarnation == incarnation {
			delete(f.observations, k)
		}
	}
	for k, e := range f.entries {
		if e.env.Request.AddonID == addon && e.incarnation == incarnation {
			delete(f.entries, k)
		}
	}
}
