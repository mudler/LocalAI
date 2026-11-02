// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"time"

	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// CaptureBackendFence binds a backend-issued generation (not the queue generation)
// to the durable queue owner. Revisions may advance within that same claim.
type CaptureBackendFence struct {
	Owner      string
	Generation int
	Fence      lagi.MemoryFence
}

// CaptureMemoryStore is the bounded worker handoff seam. The clock is sampled
// inside the chat lock, including after reservation. Callbacks must use their
// supplied context for synchronous authorization reads (GetChat and agents.Get).
// Other store reentry and retaining the callback context are not supported.
// Backend mutations must use a separate connection/pool.
// JSON retains its existing single-process directory ownership contract.
type CaptureMemoryStore interface {
	// ApplyCaptureTimeout derives the only permitted expired transition under the
	// same lock as takeover. Now is the actual effect completion time, not a lease renewal.
	ApplyCaptureTimeout(context.Context, CaptureClaim) (memory.Transition, error)
	ReserveCaptureMemory(context.Context, CaptureClaim, func() time.Time, func(context.Context, string, string) (lagi.MemoryFence, error)) (lagi.MemoryFence, error)
	WithCaptureMemory(context.Context, CaptureClaim, func() time.Time, func(context.Context, lagi.MemoryFence) error) error
}

func currentMemoryClaim(d *captureData, claim CaptureClaim, now time.Time) (CaptureRecord, error) {
	r, ok := d.Records[claim.Record.ID]
	c := claim.Record
	if !ok || r.Identity != c.Identity || r.Revision != c.Revision || r.Owner == "" || r.Owner != c.Owner || r.Generation != c.Generation || !now.Before(r.Deadline) {
		return CaptureRecord{}, ErrCaptureConflict
	}
	return r, nil
}
func associatedMemoryFence(r CaptureRecord) bool {
	a := r.BackendFence
	return a != nil && a.Owner == r.Owner && a.Generation == r.Generation && a.Fence.Key == r.ID && a.Fence.Owner == r.Owner && a.Fence.Generation != 0
}

// Reservation runs under the SAME durable chat row transaction as ClaimCapture.
// A paused old reserver therefore cannot increment the backend fence after a new
// owner has installed its fence. An orphan backend reservation after rollback or
// crash is harmless: no association is published, and recovery reserves afresh.
// This does not make queue lease expiry atomic with an already started backend
// commit. WithCaptureMemory serializes that commit with takeover; a commit that
// began while valid may finish after the deadline before takeover can proceed.
func reserveCaptureMemory(ctx context.Context, claim CaptureClaim, clock func() time.Time, reserve func(context.Context, string, string) (lagi.MemoryFence, error), edit captureEdit) (lagi.MemoryFence, error) {
	var out lagi.MemoryFence
	if clock == nil || reserve == nil {
		return out, ErrCaptureConflict
	}
	err := edit(ctx, claim.Record.Identity.Principal, claim.Record.Identity.Chat, func(d *captureData, state *chatState) error {
		ctx := state.captureContext
		r, err := currentMemoryClaim(d, claim, clock())
		if err != nil {
			return err
		}
		if associatedMemoryFence(r) {
			out = r.BackendFence.Fence
			return nil
		}
		f, err := reserve(ctx, r.ID, r.Owner)
		if err != nil {
			return err
		}
		if _, err = currentMemoryClaim(d, claim, clock()); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if f.Key != r.ID || f.Owner != r.Owner || f.Generation == 0 {
			return ErrCaptureConflict
		}
		r.BackendFence = &CaptureBackendFence{Owner: r.Owner, Generation: r.Generation, Fence: f}
		d.Records[r.ID] = r
		out = f
		return nil
	})
	if err != nil {
		return lagi.MemoryFence{}, err
	}
	return out, nil
}
func withCaptureMemory(ctx context.Context, claim CaptureClaim, clock func() time.Time, write func(context.Context, lagi.MemoryFence) error, edit captureEdit) error {
	if clock == nil || write == nil {
		return ErrCaptureConflict
	}
	return edit(ctx, claim.Record.Identity.Principal, claim.Record.Identity.Chat, func(d *captureData, state *chatState) error {
		ctx := state.captureContext
		r, err := currentMemoryClaim(d, claim, clock())
		if err != nil {
			return err
		}
		if !associatedMemoryFence(r) {
			return ErrCaptureConflict
		}
		return write(ctx, r.BackendFence.Fence)
	})
}
func (s *jsonStore) ReserveCaptureMemory(ctx context.Context, c CaptureClaim, clock func() time.Time, reserve func(context.Context, string, string) (lagi.MemoryFence, error)) (lagi.MemoryFence, error) {
	return reserveCaptureMemory(ctx, c, clock, reserve, s.editCapture)
}
func (s *gormStore) ReserveCaptureMemory(ctx context.Context, c CaptureClaim, clock func() time.Time, reserve func(context.Context, string, string) (lagi.MemoryFence, error)) (lagi.MemoryFence, error) {
	return reserveCaptureMemory(ctx, c, clock, reserve, s.editCapture)
}
func (s *jsonStore) WithCaptureMemory(ctx context.Context, c CaptureClaim, clock func() time.Time, write func(context.Context, lagi.MemoryFence) error) error {
	return withCaptureMemory(ctx, c, clock, write, s.editCapture)
}
func (s *gormStore) WithCaptureMemory(ctx context.Context, c CaptureClaim, clock func() time.Time, write func(context.Context, lagi.MemoryFence) error) error {
	return withCaptureMemory(ctx, c, clock, write, s.editCapture)
}

// JSON has single-process directory ownership. Read the locked snapshot rather
// than attempting to reacquire its non-reentrant chat lock during authorization.
type captureChatReadKey struct{}
type captureChatSnapshot struct {
	store  *jsonStore
	record session.ChatRecord
}

func withCaptureChatRead(ctx context.Context, s *jsonStore, rec session.ChatRecord) context.Context {
	return context.WithValue(ctx, captureChatReadKey{}, captureChatSnapshot{s, cloneRec(rec)})
}
func captureChatRead(ctx context.Context, s *jsonStore, principal, id string) (session.ChatRecord, bool) {
	r, ok := ctx.Value(captureChatReadKey{}).(captureChatSnapshot)
	if !ok || r.store != s || r.record.ID != id {
		return session.ChatRecord{}, false
	}
	return cloneRec(r.record), true
}

// Expiry does not surrender a claim to a late success. Only this timeout path
// may consume an expired fence, and only until a competing takeover changes it.
func applyCaptureTimeout(ctx context.Context, claim CaptureClaim, edit captureEdit) (memory.Transition, error) {
	var out memory.Transition
	c := claim.Record
	err := edit(ctx, c.Identity.Principal, c.Identity.Chat, func(d *captureData, _ *chatState) error {
		r, ok := d.Records[c.ID]
		if !ok || r.Identity != c.Identity || r.Revision != c.Revision || r.Owner == "" || r.Owner != c.Owner || r.Generation != c.Generation || claim.Now.Before(r.Deadline) {
			return ErrCaptureConflict
		}
		state, err := r.ReducerState()
		if err != nil {
			return err
		}
		w, active := stateWork(state)
		if !active || claim.Now.Unix() < w.Deadline {
			return ErrCaptureConflict
		}
		env := w.Fence
		env.Now = claim.Now.Unix()
		tr := memory.Reduce(state, memory.Timeout{Envelope: env})
		if tr.IgnoredReason != "" || (tr.RuleID != "R06-wait" && tr.RuleID != "R06-exhausted") {
			return ErrCaptureConflict
		}
		next, err := project(r, tr.State, tr.RuleID)
		if err != nil {
			return err
		}
		d.Records[c.ID] = next
		out = tr
		return nil
	})
	if err != nil {
		return memory.Transition{}, err
	}
	return out, nil
}
func (s *jsonStore) ApplyCaptureTimeout(ctx context.Context, c CaptureClaim) (memory.Transition, error) {
	return applyCaptureTimeout(ctx, c, s.editCapture)
}
func (s *gormStore) ApplyCaptureTimeout(ctx context.Context, c CaptureClaim) (memory.Transition, error) {
	return applyCaptureTimeout(ctx, c, s.editCapture)
}
