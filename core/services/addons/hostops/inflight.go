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
	mu      sync.Mutex
	entries map[string]*live
	// reserved counts, per add-on, the places of the leases that starts
	// in flight may make (ReserveLease).
	reserved     map[string]int
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
	// lease keeps the entry live after its request ended, for the background
	// job the request started (see Lease). Nil for an ordinary request.
	lease *jobLease
}

// jobLease is the job a leased entry stands for and when the lease ends.
type jobLease struct {
	job   string
	until time.Time
}

// MaxLeasesPerAddon bounds the job leases of one add-on: twice the jobs one
// add-on process may run, so a lease that waits for its end report never
// blocks a new job, and a misbehaving add-on cannot grow the table.
const MaxLeasesPerAddon = 64

// LeaseOps are the host operations a job lease allows: the job's final
// checkpoint and its network checks. Anything else needs a request that runs.
var LeaseOps = map[string]bool{"workspace.commit": true, "workspace.changes": true, "egress.check": true, "egress.audit": true}

func NewInFlight() *InFlight {
	return &InFlight{entries: map[string]*live{}, observations: map[string]*observation{}, reserved: map[string]int{}}
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
				if e.n--; e.n <= 0 && e.lease == nil {
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
	env, b, _, ok := f.LookupLive(addonID, operationID)
	return env, b, ok
}

// LookupLive is LookupBound that also says whether only a job lease keeps the
// entry live: the request that started the job has ended, so nobody waits on
// a turn of the chat. A lease past its end is not live.
func (f *InFlight) LookupLive(addonID, operationID string) (env contract.Envelope, b Binding, leased, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entries[key(addonID, operationID)]
	if e == nil || e.mixed {
		return contract.Envelope{}, Binding{}, false, false
	}
	now := time.Now()
	if e.n > 0 {
		if !e.env.Authority.ExpiresAt.After(now) {
			return contract.Envelope{}, Binding{}, false, false
		}
		return e.env, e.binding, false, true
	}
	if e.lease == nil || !e.lease.until.After(now) {
		return contract.Envelope{}, Binding{}, false, false
	}
	return e.env, e.binding, true, true
}

// Lease keeps the entry of a request that runs now live after the request
// ends, until the job it started is reported ended (EndLease), until until,
// or until the runtime incarnation goes away (ForgetRuntime). Only a request
// that runs can be leased, by its own incarnation, once: an add-on cannot
// make a lease for a request the host did not send. A lease holds the same
// actor, scope, resource and chat as the request, and allows only LeaseOps.
func (f *InFlight) Lease(addonID, operationID, incarnation, job string, until time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leaseLocked(addonID, operationID, incarnation, job, until, false)
}

// ErrLeasesFull says the add-on holds as many job leases as it may.
var ErrLeasesFull = errors.New("too many job leases")

// liveLeasesLocked drops the expired leases nobody ended and counts the live
// ones of the add-on.
func (f *InFlight) liveLeasesLocked(addonID string, now time.Time) int {
	n := 0
	for k, e := range f.entries {
		if e.lease == nil {
			continue
		}
		if e.n <= 0 && !e.lease.until.After(now) {
			delete(f.entries, k) // an expired lease nobody ended
			continue
		}
		if e.env.Request.AddonID == addonID {
			n++
		}
	}
	return n
}

func (f *InFlight) leaseLocked(addonID, operationID, incarnation, job string, until time.Time, slot bool) error {
	now := time.Now()
	n := f.liveLeasesLocked(addonID, now) + f.reserved[addonID]
	e := f.entries[key(addonID, operationID)]
	switch {
	case e == nil || e.n <= 0 || e.mixed || e.observation != nil:
		return errors.New("no request runs under this operation")
	case e.incarnation != incarnation:
		return errors.New("another incarnation")
	case e.lease != nil:
		return errors.New("the request already holds a lease")
	case job == "" || !until.After(now):
		return errors.New("a lease needs a job and an end")
	case !slot && n >= MaxLeasesPerAddon:
		return ErrLeasesFull
	}
	e.lease = &jobLease{job: job, until: until}
	return nil
}

// LeaseSlot is the place of a job lease, taken before the start of a job is
// sent: the add-on starts no job whose lease the host would refuse. Exactly
// one of Lease and Release frees the place; later calls do nothing.
type LeaseSlot struct {
	f     *InFlight
	addon string
	used  bool
}

// ReserveLease takes the place of the lease of a job that a start call may
// make. With every place of the add-on taken or reserved it is ErrLeasesFull,
// and the start must not be sent.
func (f *InFlight) ReserveLease(addonID string) (*LeaseSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.liveLeasesLocked(addonID, time.Now())+f.reserved[addonID] >= MaxLeasesPerAddon {
		return nil, ErrLeasesFull
	}
	f.reserved[addonID]++
	return &LeaseSlot{f: f, addon: addonID}, nil
}

// Lease is InFlight.Lease within the reserved place.
func (s *LeaseSlot) Lease(operationID, incarnation, job string, until time.Time) error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if s.used {
		return errors.New("the place of the lease was given back")
	}
	s.used = true
	s.f.reserved[s.addon]--
	return s.f.leaseLocked(s.addon, operationID, incarnation, job, until, true)
}

// Release gives the place back: the start was refused or made no job.
func (s *LeaseSlot) Release() {
	if s == nil {
		return
	}
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if !s.used {
		s.used = true
		s.f.reserved[s.addon]--
	}
}

// EndLease ends the lease of a job of an add-on incarnation. It is safe to
// call for a job without a lease.
func (f *InFlight) EndLease(addonID, incarnation, job string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, e := range f.entries {
		if e.lease != nil && e.lease.job == job && e.env.Request.AddonID == addonID && e.incarnation == incarnation {
			e.lease = nil
			if e.n <= 0 {
				delete(f.entries, k)
			}
		}
	}
}

// Leases is the number of live job leases of an add-on.
func (f *InFlight) Leases(addonID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, now := 0, time.Now()
	for _, e := range f.entries {
		if e.lease != nil && e.env.Request.AddonID == addonID && (e.n > 0 || e.lease.until.After(now)) {
			n++
		}
	}
	return n
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
