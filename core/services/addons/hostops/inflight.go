// SPDX-License-Identifier: MIT

// Package hostops serves the host operation channel: the calls an add-on
// process makes to ask the host to do what the add-on cannot do itself.
package hostops

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// InFlight holds the envelope of every request the host is serving for an
// add-on right now. A host operation is allowed only while its operation ID
// is in here, so an add-on cannot act when no user asked it to.
type InFlight struct {
	mu      sync.Mutex
	entries map[string]*live
}

type live struct {
	env contract.Envelope
	// binding is the chat the request was made for; bound is false for a
	// request no chat made.
	binding Binding
	bound   bool
	// mixed is set when two requests share the operation ID with different
	// bindings. Neither chat can be trusted, so the entry is not live.
	mixed bool
	n     int
}

func NewInFlight() *InFlight { return &InFlight{entries: map[string]*live{}} }

func key(addonID, operationID string) string { return addonID + "/" + operationID }

// Begin records the envelope of a request that no chat made. The returned
// function removes it; it is safe to call more than once. Two requests with
// the same operation ID share one entry until both end.
func (f *InFlight) Begin(env contract.Envelope) (end func()) {
	return f.begin(env, Binding{}, false)
}

// BeginBound records the envelope of a request made for the chat b. The
// binding stays on the host: the envelope the add-on receives is unchanged.
func (f *InFlight) BeginBound(env contract.Envelope, b Binding) (end func()) {
	return f.begin(env, b, true)
}

func (f *InFlight) begin(env contract.Envelope, b Binding, bound bool) (end func()) {
	k := key(env.Request.AddonID, env.Request.OperationID)
	f.mu.Lock()
	if e := f.entries[k]; e != nil {
		e.n++
		e.env = env
		if e.bound != bound || e.binding != b {
			e.mixed = true
		}
	} else {
		f.entries[k] = &live{env: env, binding: b, bound: bound, n: 1}
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
