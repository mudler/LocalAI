// SPDX-License-Identifier: MIT
package runner

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	pc "github.com/mudler/LocalAI/core/services/agentruntime/privatecheckpoint"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
)

var errShell = errors.New("runner: operation unavailable")

// These are trusted host seams, not production implementations. In particular,
// Settle must synchronize current authority, lease epoch, revision and active
// turn with the durable ciphertext write. Check followed by Commit is invalid.
type nativeSession interface {
	// Run must acquire the single-use permit before executing. Cancel is sticky:
	// cancellation before native entry must prevent inference, even after admission.
	Run(context.Context, []byte, *startPermit) error
	Cancel()
	AwaitQuiescence(context.Context) error
	Export() (nibadapter.Checkpoint, []byte, error)
	Close() error
}
type nativeSessionFactory interface {
	New(context.Context, identity, *pc.PrivatePayload, func()) (nativeSession, error)
}
type currentAuthority interface {
	Check(context.Context, identity) error
}
type checkpointProvenance interface {
	SaveRequest(context.Context, identity) (pc.SaveRequest, error)
}
type trustedSettlement interface {
	Settle(context.Context, settlementRequest) (*handoff, error)
}
type trustedHandoff interface {
	StartNext(context.Context, handoff) error
}
type trustedClock interface{ Now() uint64 }
type settlementRequest struct {
	redacted
	id         identity
	checkpoint store.Checkpoint
	outcome    completion
}

// A handoff is data for a NEW coordinator, never a mutation of the settled one.
type handoff struct {
	redacted
	initial data
}
type shellDependencies struct {
	Factory    nativeSessionFactory
	Authority  currentAuthority
	Provenance checkpointProvenance
	Settlement trustedSettlement
	Handoff    trustedHandoff
	Clock      trustedClock
	Codec      *pc.Service
}
type coordinator struct {
	redacted
	open func() *coordinatorFields
}

func (c *coordinator) fields() *coordinatorFields { return c.open() }

type coordinatorFields struct {
	mu       sync.Mutex
	state    runnerState
	deps     shellDependencies
	ctx      context.Context
	session  nativeSession
	joined   bool
	restored func() *pc.PrivatePayload
	permit   *startPermit
	next     *handoff
	workers  sync.WaitGroup
}

func missing(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func:
		return r.IsNil()
	}
	return false
}
func newCoordinator(ctx context.Context, initial data, d shellDependencies, restored *pc.PrivatePayload) (*coordinator, error) {
	if ctx == nil || ctx.Err() != nil || CheckInvariants(idle{initial}) != nil {
		return nil, errShell
	}
	for _, v := range []any{d.Factory, d.Authority, d.Provenance, d.Settlement, d.Handoff, d.Clock, d.Codec} {
		if missing(v) {
			return nil, errShell
		}
	}
	// Trusted injection only: the host must authorize Codec.Restore for this target.
	// Own a clone before retaining it; caller replacement cannot change this attempt.
	var owned *pc.PrivatePayload
	if restored != nil {
		p, e := pc.NewPrivatePayload(restored.NativeCheckpoint(), restored.Cursor())
		if e != nil {
			return nil, errShell
		}
		owned = &p
	}
	f := &coordinatorFields{ctx: ctx, state: idle{initial.clone()}, deps: d, restored: func() *pc.PrivatePayload { return owned }, permit: &startPermit{}}
	c := &coordinator{open: func() *coordinatorFields { return f }}
	c.fields().workers.Add(1)
	go func() { defer c.fields().workers.Done(); <-ctx.Done(); _ = c.leaseLost() }()
	return c, nil
}
func (c *coordinator) waitWorkers() { c.fields().workers.Wait() }
func (c *coordinator) phase() string {
	c.fields().mu.Lock()
	defer c.fields().mu.Unlock()
	return c.fields().state.phase()
}
func (c *coordinator) input() []byte {
	c.fields().mu.Lock()
	defer c.fields().mu.Unlock()
	return slices.Clone(c.fields().state.payload().fields().input)
}
func (c *coordinator) host(n string) error {
	c.fields().mu.Lock()
	id := c.fields().state.payload().fields().id.clone()
	c.fields().mu.Unlock()
	return c.deliver(n, id, nil)
}
func (c *coordinator) begin() error     { return c.host("Begin") }
func (c *coordinator) detach() error    { return c.host("Detach") }
func (c *coordinator) stop() error      { return c.host("Stop") }
func (c *coordinator) leaseLost() error { return c.host("LeaseLost") }
func (c *coordinator) timeout() error   { return c.host("CancelTimedOut") }
func (c *coordinator) unknown() error   { return c.host("UnknownJournal") }
func (c *coordinator) deliver(n string, id identity, b []byte) error {
	c.fields().mu.Lock()
	defer c.fields().mu.Unlock()
	e := makeEvent(n, ownEventData(&eventFields{id: id, now: c.fields().deps.Clock.Now(), trusted: true, content: b}))
	t := PureReduce(c.fields().state, e)
	if CheckTransition(c.fields().state, e, t) != nil {
		return errShell
	}
	if c.fields().state.phase() == "Running" && t.state.phase() != "Running" {
		c.fields().permit.invalidate()
	}
	c.fields().state = t.state
	for _, fx := range t.effects {
		c.execute(fx)
	}
	return nil
}

// Called under the coordinator lock. Slow work runs outside it; its result is
// reduced again against the current phase and captured immutable identity.
func (c *coordinator) async(fn func()) {
	c.fields().workers.Add(1)
	go func() { defer c.fields().workers.Done(); fn() }()
}
func (c *coordinator) execute(fx effect) {
	id := fx.payload().fields().id.clone()
	switch fx.(type) {
	case restoreSessionEffect:
		c.async(func() {
			if c.fields().deps.Authority.Check(c.fields().ctx, id) != nil {
				_ = c.deliver("RestoreFailed", id, nil)
				return
			}
			s, e := c.fields().deps.Factory.New(c.fields().ctx, id, c.fields().restored(), func() { _ = c.deliver("Stream", id, nil) })
			if e != nil || missing(s) {
				_ = c.deliver("RestoreFailed", id, nil)
				return
			}
			c.fields().mu.Lock()
			if c.fields().state.phase() != "Restoring" || !c.fields().state.payload().fields().id.equal(id) {
				c.fields().mu.Unlock()
				s.Cancel()
				if s.AwaitQuiescence(c.fields().ctx) == nil {
					_ = s.Close()
				}
				return
			}
			c.fields().session = s
			c.fields().mu.Unlock()
			_ = c.deliver("RestoreSucceeded", id, nil)
		})
	case startTurnEffect:
		s := c.fields().session
		input := slices.Clone(c.fields().state.payload().fields().input)
		c.async(func() {
			if c.fields().deps.Authority.Check(c.fields().ctx, id) != nil {
				_ = c.deliver("LeaseLost", id, nil)
				return
			}
			c.fields().mu.Lock()
			valid := c.fields().state.phase() == "Running" && c.fields().state.payload().fields().id.equal(id) && c.fields().ctx.Err() == nil
			permit := c.fields().permit
			c.fields().mu.Unlock()
			if !valid {
				return
			}
			// The permit closes the unlocked check-to-native-entry window. Never hold
			// c.fields().mu over Run: Stop must be able to cancel a blocked native turn.
			if s.Run(c.fields().ctx, input, permit) != nil {
				_ = c.deliver("UnknownJournal", id, nil)
				return
			}
			_ = c.deliver("TurnFinished", id, nil)
		})
	case cancelSessionEffect:
		if c.fields().session != nil {
			c.fields().session.Cancel()
		}
	case awaitQuiescenceEffect:
		s := c.fields().session
		if s == nil {
			return
		}
		c.async(func() {
			if s.AwaitQuiescence(c.fields().ctx) != nil {
				_ = c.deliver("CancelTimedOut", id, nil)
				return
			}
			c.fields().mu.Lock()
			c.fields().joined = true
			c.fields().mu.Unlock()
			_ = c.deliver("QuiescenceConfirmed", id, nil)
		})
	case exportAndSealEffect:
		s := c.fields().session
		if !c.fields().joined || s == nil {
			return
		}
		c.async(func() {
			cp, cursor, e := s.Export()
			if e != nil {
				_ = c.deliver("SealFailed", id, nil)
				return
			}
			p, e := pc.NewPrivatePayload(cp, cursor)
			if e != nil {
				_ = c.deliver("SealFailed", id, nil)
				return
			}
			r, e := c.fields().deps.Provenance.SaveRequest(c.fields().ctx, id)
			f := id.fields()
			target := pc.Scope{Principal: f.principal, Conversation: f.conversation, Turn: f.turn, Attempt: f.attempt, Epoch: f.epoch}
			if e != nil || r.Target != target || r.Binding.Source != target {
				_ = c.deliver("SealFailed", id, nil)
				return
			}
			r.Quiescent = true
			sealed, e := c.fields().deps.Codec.Seal(c.fields().ctx, r, p)
			if e != nil {
				_ = c.deliver("SealFailed", id, nil)
				return
			}
			_ = c.deliver("SealSucceeded", id, sealed.StoreCheckpoint().Context)
		})
	case settleCiphertextEffect:
		r := settlementRequest{id: id, checkpoint: store.Checkpoint{Version: 1, Context: slices.Clone(fx.payload().fields().content)}, outcome: c.fields().state.payload().fields().outcome}
		c.async(func() {
			next, e := c.fields().deps.Settlement.Settle(c.fields().ctx, r)
			if e != nil {
				_ = c.deliver("SettleFailed", id, nil)
				return
			}
			c.fields().mu.Lock()
			c.fields().next = next
			c.fields().mu.Unlock()
			_ = c.deliver("SettleSucceeded", id, nil)
		})
	case startNextEffect:
		if c.fields().next != nil {
			next := handoff{initial: c.fields().next.initial.clone()}
			old := id.fields()
			fresh := next.initial.fields().id.fields()
			if fresh != nil && fresh.principal == old.principal && fresh.conversation == old.conversation && !next.initial.fields().id.equal(id) && len(next.initial.fields().input) > 0 {
				c.async(func() { _ = c.fields().deps.Handoff.StartNext(c.fields().ctx, next) })
			}
		}
	case closeSessionEffect:
		if c.fields().session != nil && c.fields().joined {
			s := c.fields().session
			c.async(func() { _ = s.Close() })
		}
	case emitSafeEffect, holdInputEffect:
		// No raw callback payload or checkpoint becomes a public event.
	}
}
func shellDiagnostic(c *coordinator) string { return fmt.Sprint(c) }

// startPermit linearizes cancellation against native execution admission.
// It is local attempt state, not provider fencing or durable authority.
type startPermit struct {
	mu                sync.Mutex
	invalid, admitted bool
}

func (p *startPermit) enter(ctx context.Context) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.invalid || p.admitted || ctx.Err() != nil {
		return false
	}
	p.admitted = true
	return true
}
func (p *startPermit) invalidate() { p.mu.Lock(); p.invalid = true; p.mu.Unlock() }
