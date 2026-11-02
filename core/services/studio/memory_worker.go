// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// CaptureMemory is optional: synchronous tool writes alone do not establish a
// fenced automatic-capture backend. Implementations must not retain contexts.
// An adapter may additionally implement CaptureReady() bool to defer claims
// during dependency startup, without treating unavailability as revoked access.
type CaptureMemory interface {
	BeginCapture(context.Context, memory.Identity, string, string, func(context.Context) error) (lagi.MemoryFence, error)
	ReconcileCapture(context.Context, memory.Identity, string) (lagi.MemoryReceipt, error)
	WriteCapture(context.Context, memory.Identity, lagi.MemoryFence, lagi.MemoryWrite, func(context.Context) error) (lagi.MemoryReceipt, error)
}

// CaptureWorkerOptions permits deterministic clocks and distinct instance IDs.
// Publish is live status only; durable capture records remain the source of truth.
// It runs after store callbacks return and must not block.
type CaptureWorkerOptions struct {
	Now     func() time.Time
	Owner   string
	Publish func(store.CaptureRecord, memory.PublishStatus)
}

// AuthorizeCapture reloads current access and policy; frozen routing never grants
// permission, and changing destination/mode cannot redirect an old capture.
func (s *Service) AuthorizeCapture(ctx context.Context, i memory.Identity) error {
	ag, err := s.memoryAgent(ctx, userChatActor(i.Principal, i.Chat))
	if err != nil {
		return err
	}
	if ag.ID != i.Agent || ag.Memory.Destination != i.Destination {
		return ErrMemoryDenied
	}
	mode := ag.Memory.AutomaticIngestion
	switch i.Kind {
	case "incoming":
		if mode != "incoming_only" && mode != "incoming_and_replies" {
			return ErrMemoryDenied
		}
	case "reply":
		if mode != "incoming_and_replies" {
			return ErrMemoryDenied
		}
	case "summary":
		if mode != "summary" {
			return ErrMemoryDenied
		}
	default:
		return ErrMemoryDenied
	}
	names, err := s.d.Collections.List(ctx, i.Principal)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == i.Destination {
			return ctx.Err()
		}
	}
	return ErrMemoryDenied
}

// ProcessMemoryCaptures owns no goroutines. The caller owns its lifetime and
// joins it by waiting for this call after cancellation. Idle/error polling is
// bounded; retry timing and exhaustion are exclusively reducer/store decisions.
func (s *Service) ProcessMemoryCaptures(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	backend, ok := s.d.Collections.(CaptureMemory)
	fenced, supported := s.d.Store.(store.CaptureMemoryStore)
	if !ok || !supported {
		return lagi.ErrMemoryUnsupported
	}
	options := s.d.CaptureWorker
	now := options.Now
	if now == nil {
		now = time.Now
	}
	owner := options.Owner
	if owner == "" {
		owner = uuid.NewString()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Startup readiness is not an authorization decision. Do not consume a
		// claim (including expired Writing recovery) until the adapter can check
		// live permissions and reconcile the backend.
		var claim store.CaptureClaim
		err := store.ErrNoCapture
		if ready, ok := s.d.Collections.(interface{ CaptureReady() bool }); !ok || ready.CaptureReady() {
			claim, err = s.d.Store.ClaimCapture(ctx, owner, now(), 60*time.Second)
		}
		if err == nil {
			err = s.runCapture(ctx, claim, backend, fenced, now, options.Publish)
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) runCapture(ctx context.Context, claim store.CaptureClaim, backend CaptureMemory, fenced store.CaptureMemoryStore, now func() time.Time, publish func(store.CaptureRecord, memory.PublishStatus)) error {
	tr := claim.Transition
	for len(tr.Effects) > 0 {
		// The transition is already durable. Publish its projection before any
		// effect can apply the next revision, regardless of reducer effect order.
		// Store callbacks have returned: subscribers may safely reload the record.
		for _, effect := range tr.Effects {
			if status, ok := effect.(memory.PublishStatus); ok && publish != nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				publish(claim.Record, status)
			}
		}
		var next *memory.Transition
		for _, effect := range tr.Effects {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := effect.(memory.PublishStatus); ok {
				continue
			}
			if _, ok := effect.(memory.ScheduleRetry); ok {
				continue
			}
			event, err := s.captureEffect(ctx, claim, effect, backend, fenced, now)
			if err != nil {
				return err
			}
			state, err := claim.Record.ReducerState()
			if err != nil {
				return err
			}
			// Sample completion time once. An expired result can only become a
			// store-derived timeout, never a late success or a backdated apply.
			claim.Now = now()
			var transition memory.Transition
			if w, active := captureWork(state); active && claim.Now.Unix() >= w.Deadline {
				transition, err = fenced.ApplyCaptureTimeout(ctx, claim)
			} else {
				transition = memory.Reduce(state, event)
				if transition.IgnoredReason != "" {
					return store.ErrCaptureConflict
				}
				err = s.d.Store.ApplyCapture(ctx, claim, transition)
			}
			if err != nil {
				return err
			}
			// Reload the persisted projection: the store can shorten phase deadlines.
			records, err := s.d.Store.ListCaptures(ctx, claim.Record.Identity.Principal, claim.Record.Identity.Chat)
			if err != nil {
				return err
			}
			found := false
			for _, record := range records {
				if record.ID == claim.Record.ID && record.Revision == claim.Record.Revision+1 {
					claim.Record = record
					found = true
					break
				}
			}
			if !found {
				return store.ErrCaptureConflict
			}
			next = &transition
		}
		if next == nil {
			return nil
		}
		tr = *next
	}
	return nil
}

func (s *Service) captureEffect(ctx context.Context, claim store.CaptureClaim, effect memory.Effect, backend CaptureMemory, fenced store.CaptureMemoryStore, now func() time.Time) (memory.Event, error) {
	// Extract the active work from the persisted state, not an in-memory deadline.
	state, err := claim.Record.ReducerState()
	if err != nil {
		return nil, err
	}
	var w memory.Work
	switch v := state.(type) {
	case memory.Authorizing:
		w = v.Work
	case memory.Transforming:
		w = v.Work
	case memory.PersistingPayload:
		w = v.Work
	case memory.Writing:
		w = v.Work
	default:
		return nil, store.ErrCaptureConflict
	}
	effectCtx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Unix(w.Deadline, 0).Sub(now())))
	defer cancel()
	env := w.Fence
	env.Now = now().Unix()
	if env.Now >= w.Deadline {
		return memory.Timeout{Envelope: env}, nil
	}
	authorize := func(c context.Context) error { return s.AuthorizeCapture(c, w.Job.Identity) }
	switch v := effect.(type) {
	case memory.CheckAuthorization:
		err := authorize(effectCtx)
		env.Now = now().Unix()
		if err != nil {
			return memory.AuthorizationDenied{Envelope: env}, nil
		}
		return memory.AuthorizationGranted{Envelope: env}, nil
	case memory.Summarize:
		if err := authorize(effectCtx); err != nil {
			env.Now = now().Unix()
			return memory.Revoked{Envelope: env}, nil
		}
		input, ok := w.Job.Input.(memory.SummaryInput)
		if !ok {
			return nil, store.ErrCaptureConflict
		}
		text, err := s.SummarizeMemory(effectCtx, SummaryRequest{Principal: w.Job.Identity.Principal, ChatID: w.Job.Identity.Chat, Content: input.Text})
		env.Now = now().Unix()
		if err != nil {
			return memory.SummaryFailed{Envelope: env}, nil
		}
		return memory.SummarySucceeded{Envelope: env, Text: text}, nil
	case memory.PersistPayload:
		// The preceding transition durably stores PendingPayload. Its acknowledgment
		// is another durable transition before the reducer can emit a backend write.
		if !validMemoryContent(v.Payload.Text) {
			return memory.PayloadPersistFailed{Envelope: env}, nil
		}
		return memory.PayloadPersisted{Envelope: env}, nil
	case memory.ReconcileOrWrite:
		_, err := fenced.ReserveCaptureMemory(effectCtx, claim, now, func(c context.Context, key, owner string) (lagi.MemoryFence, error) {
			return backend.BeginCapture(c, w.Job.Identity, key, owner, authorize)
		})
		var receipt lagi.MemoryReceipt
		if err == nil {
			err = fenced.WithCaptureMemory(effectCtx, claim, now, func(c context.Context, fence lagi.MemoryFence) error {
				if err := authorize(c); err != nil {
					return err
				}
				var err error
				receipt, err = backend.ReconcileCapture(c, w.Job.Identity, claim.Record.ID)
				if err != nil {
					return err
				}
				if receipt.State == lagi.MemoryDeleted || receipt.State == lagi.MemorySaved {
					return nil
				}
				payload, ok := w.Job.Input.(memory.FrozenPayload)
				if !ok {
					return store.ErrCaptureConflict
				}
				metadata := map[string]string{"agent_id": w.Job.Identity.Agent, "chat_id": w.Job.Identity.Chat, "capture_source": w.Job.Identity.Kind}
				hash, err := lagi.MemoryPayloadHash(payload.Text, metadata)
				if err != nil {
					return err
				}
				receipt, err = backend.WriteCapture(c, w.Job.Identity, fence, lagi.MemoryWrite{Key: claim.Record.ID, Content: payload.Text, Metadata: metadata, PayloadHash: hash}, authorize)
				return err
			})
		}
		env.Now = now().Unix()
		if errors.Is(err, ErrMemoryDenied) || errors.Is(err, ErrNoCollection) {
			return memory.Revoked{Envelope: env}, nil
		}
		if err != nil {
			return memory.WriteFailed{Envelope: env}, nil
		}
		if receipt.State == lagi.MemoryDeleted {
			return memory.WriteDeleted{Envelope: env}, nil
		}
		return memory.WriteSucceeded{Envelope: env, EntryID: receipt.EntryID}, nil
	}
	return nil, errors.New("studio: unsupported capture effect")
}

// StartMemoryCaptures starts one local interpreter. Cross-instance exclusion is
// provided by the durable store/backend fences, never by this lifecycle lock.
func (s *Service) StartMemoryCaptures(ctx context.Context) {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if s.captureDone != nil || s.isClosed() {
		return
	}
	ctx, s.captureCancel = context.WithCancel(ctx)
	s.captureDone = make(chan struct{})
	go func() { defer close(s.captureDone); _ = s.ProcessMemoryCaptures(ctx) }()
}

func captureWork(state memory.State) (memory.Work, bool) {
	switch v := state.(type) {
	case memory.Authorizing:
		return v.Work, true
	case memory.Transforming:
		return v.Work, true
	case memory.PersistingPayload:
		return v.Work, true
	case memory.Writing:
		return v.Work, true
	}
	return memory.Work{}, false
}
