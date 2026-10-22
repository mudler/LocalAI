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
	n   int
}

func NewInFlight() *InFlight { return &InFlight{entries: map[string]*live{}} }

func key(addonID, operationID string) string { return addonID + "/" + operationID }

// Begin records the envelope for the time of the request. The returned
// function removes it; it is safe to call more than once. Two requests with
// the same operation ID share one entry until both end.
func (f *InFlight) Begin(env contract.Envelope) (end func()) {
	k := key(env.Request.AddonID, env.Request.OperationID)
	f.mu.Lock()
	if e := f.entries[k]; e != nil {
		e.n++
		e.env = env
	} else {
		f.entries[k] = &live{env: env, n: 1}
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
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entries[key(addonID, operationID)]
	if e == nil || !e.env.Authority.ExpiresAt.After(time.Now()) {
		return contract.Envelope{}, false
	}
	return e.env, true
}
