// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
	"sync"
)

// Control is an opaque host handle. Do not expose it to native approval callbacks.
// The trusted Approvals dependency must authenticate actor and exact binding.
type Control struct {
	redacted
	open func() *controlSlot
}
type controlSlot struct {
	mu        sync.Mutex
	bridge    *Bridge
	handle    toolbroker.CallHandle
	candidate executionCandidate
	binding   toolbroker.Binding
	closed    bool
	wake      chan struct{}
}

func (c Control) Decide(ctx context.Context, actor toolbroker.Actor, actorID string, allow bool) error {
	if c.open == nil || (actor != toolbroker.HumanActor && actor != toolbroker.HostActor) || !identifier(actorID) {
		return ErrHold
	}
	s := c.open()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrHold
	}
	s.mu.Unlock()
	a := toolbroker.Approval{Binding: s.binding, Actor: actor, ActorID: actorID}
	_, err := s.bridge.broker.Decide(withOperation(ctx, s.candidate, true), toolbroker.ApprovalDecision{Handle: s.handle, Approval: a, Allow: allow})
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if err != nil {
		return ErrHold
	}
	return nil
}
func (c Control) Cancel(ctx context.Context) error {
	if c.open == nil {
		return ErrHold
	}
	s := c.open()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrHold
	}
	s.mu.Unlock()
	err := s.bridge.broker.Cancel(ctx, s.handle, toolbroker.CancelUser)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if err != nil {
		return ErrHold
	}
	return nil
}
func (b *Bridge) closeSlot(s *controlSlot) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	b.mu.Lock()
	delete(b.active, s)
	b.mu.Unlock()
}
func (b *Bridge) Close() {
	b.mu.Lock()
	b.closed = true
	slots := make([]*controlSlot, 0, len(b.active))
	for s := range b.active {
		slots = append(slots, s)
	}
	b.mu.Unlock()
	for _, s := range slots {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		_ = b.broker.Cancel(context.Background(), s.handle, toolbroker.CancelUser)
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}
