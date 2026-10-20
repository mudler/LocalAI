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
	Run(context.Context, []byte) error
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
	mu       sync.Mutex
	state    runnerState
	deps     shellDependencies
	ctx      context.Context
	session  nativeSession
	joined   bool
	restored *pc.PrivatePayload
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
	c := &coordinator{ctx: ctx, state: idle{initial.clone()}, deps: d, restored: restored}
	c.workers.Add(1)
	go func() { defer c.workers.Done(); <-ctx.Done(); _ = c.leaseLost() }()
	return c, nil
}
func (c *coordinator) waitWorkers()  { c.workers.Wait() }
func (c *coordinator) phase() string { c.mu.Lock(); defer c.mu.Unlock(); return c.state.phase() }
func (c *coordinator) input() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.state.payload().fields().input)
}
func (c *coordinator) host(n string) error {
	c.mu.Lock()
	id := c.state.payload().fields().id.clone()
	c.mu.Unlock()
	return c.deliver(n, id, nil)
}
func (c *coordinator) begin() error     { return c.host("Begin") }
func (c *coordinator) detach() error    { return c.host("Detach") }
func (c *coordinator) stop() error      { return c.host("Stop") }
func (c *coordinator) leaseLost() error { return c.host("LeaseLost") }
func (c *coordinator) timeout() error   { return c.host("CancelTimedOut") }
func (c *coordinator) unknown() error   { return c.host("UnknownJournal") }
func (c *coordinator) deliver(n string, id identity, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := makeEvent(n, ownEventData(&eventFields{id: id, now: c.deps.Clock.Now(), trusted: true, content: b}))
	t := PureReduce(c.state, e)
	if CheckTransition(c.state, e, t) != nil {
		return errShell
	}
	c.state = t.state
	for _, fx := range t.effects {
		c.execute(fx)
	}
	return nil
}

// Called under the coordinator lock. Slow work runs outside it; its result is
// reduced again against the current phase and captured immutable identity.
func (c *coordinator) async(fn func()) {
	c.workers.Add(1)
	go func() { defer c.workers.Done(); fn() }()
}
func (c *coordinator) execute(fx effect) {
	id := fx.payload().fields().id.clone()
	switch fx.(type) {
	case restoreSessionEffect:
		c.async(func() {
			if c.deps.Authority.Check(c.ctx, id) != nil {
				_ = c.deliver("RestoreFailed", id, nil)
				return
			}
			s, e := c.deps.Factory.New(c.ctx, id, c.restored, func() { _ = c.deliver("Stream", id, nil) })
			if e != nil || missing(s) {
				_ = c.deliver("RestoreFailed", id, nil)
				return
			}
			c.mu.Lock()
			if c.state.phase() != "Restoring" || !c.state.payload().fields().id.equal(id) {
				c.mu.Unlock()
				s.Cancel()
				if s.AwaitQuiescence(c.ctx) == nil {
					_ = s.Close()
				}
				return
			}
			c.session = s
			c.mu.Unlock()
			_ = c.deliver("RestoreSucceeded", id, nil)
		})
	case startTurnEffect:
		s := c.session
		input := slices.Clone(c.state.payload().fields().input)
		c.async(func() {
			if c.deps.Authority.Check(c.ctx, id) != nil {
				_ = c.deliver("LeaseLost", id, nil)
				return
			}
			if s.Run(c.ctx, input) != nil {
				_ = c.deliver("UnknownJournal", id, nil)
				return
			}
			_ = c.deliver("TurnFinished", id, nil)
		})
	case cancelSessionEffect:
		if c.session != nil {
			c.session.Cancel()
		}
	case awaitQuiescenceEffect:
		s := c.session
		if s == nil {
			return
		}
		c.async(func() {
			if s.AwaitQuiescence(c.ctx) != nil {
				_ = c.deliver("CancelTimedOut", id, nil)
				return
			}
			c.mu.Lock()
			c.joined = true
			c.mu.Unlock()
			_ = c.deliver("QuiescenceConfirmed", id, nil)
		})
	case exportAndSealEffect:
		s := c.session
		if !c.joined || s == nil {
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
			r, e := c.deps.Provenance.SaveRequest(c.ctx, id)
			f := id.fields()
			target := pc.Scope{Principal: f.principal, Conversation: f.conversation, Turn: f.turn, Attempt: f.attempt, Epoch: f.epoch}
			if e != nil || r.Target != target || r.Binding.Source != target {
				_ = c.deliver("SealFailed", id, nil)
				return
			}
			r.Quiescent = true
			sealed, e := c.deps.Codec.Seal(c.ctx, r, p)
			if e != nil {
				_ = c.deliver("SealFailed", id, nil)
				return
			}
			_ = c.deliver("SealSucceeded", id, sealed.StoreCheckpoint().Context)
		})
	case settleCiphertextEffect:
		r := settlementRequest{id: id, checkpoint: store.Checkpoint{Version: 1, Context: slices.Clone(fx.payload().fields().content)}, outcome: c.state.payload().fields().outcome}
		c.async(func() {
			next, e := c.deps.Settlement.Settle(c.ctx, r)
			if e != nil {
				_ = c.deliver("SettleFailed", id, nil)
				return
			}
			c.mu.Lock()
			c.next = next
			c.mu.Unlock()
			_ = c.deliver("SettleSucceeded", id, nil)
		})
	case startNextEffect:
		if c.next != nil {
			next := handoff{initial: c.next.initial.clone()}
			old := id.fields()
			fresh := next.initial.fields().id.fields()
			if fresh != nil && fresh.principal == old.principal && fresh.conversation == old.conversation && !next.initial.fields().id.equal(id) && len(next.initial.fields().input) > 0 {
				c.async(func() { _ = c.deps.Handoff.StartNext(c.ctx, next) })
			}
		}
	case closeSessionEffect:
		if c.session != nil && c.joined {
			s := c.session
			c.async(func() { _ = s.Close() })
		}
	case emitSafeEffect, holdInputEffect:
		// No raw callback payload or checkpoint becomes a public event.
	}
}
func shellDiagnostic(c *coordinator) string { return fmt.Sprint(c) }
