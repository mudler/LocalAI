// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"sort"
)

// MemoryStatus is deliberately separate from CaptureRecord: queue records hold
// bodies, identities and backend fences which must never reach clients.
type MemoryStatus = schema.ChatsMemoryCapture

func projectMemoryStatus(r store.CaptureRecord) (MemoryStatus, error) {
	out := MemoryStatus{ID: r.ID, Destination: r.Identity.Destination}
	state, err := r.ReducerState()
	if err != nil {
		return out, err
	}
	switch v := state.(type) {
	case memory.Queued:
		out.Status = "queued"
	case memory.RetryWaiting:
		out.Status = "queued"
		out.NextRetry = v.Due
	case memory.Authorizing, memory.Transforming, memory.PersistingPayload, memory.Writing:
		out.Status = "processing"
	case memory.Saved:
		out.Status = "saved"
		out.EntryID = v.EntryID
	case memory.Failed:
		out.Status = "failed"
		out.Reason = safeCaptureReason(v.Reason, "capture_failed")
	case memory.Skipped:
		out.Status = "skipped"
		out.Reason = safeCaptureReason(v.Reason, "capture_skipped")
	}
	return out, nil
}

// Only reducer-owned literals become public codes. Never pass through stored
// reasons: older versions or adapters may have persisted diagnostic strings.
func safeCaptureReason(reason, fallback string) string {
	switch reason {
	case "attempts exhausted or expired", "capture expired or exhausted":
		return "exhausted_or_expired"
	case "capture expired":
		return "expired"
	case "recovery exhausted":
		return "exhausted"
	case "current policy revoked; no destination fallback":
		return "policy_revoked"
	case "current authorization denied":
		return "authorization_denied"
	case "backend tombstone; do not recreate":
		return "deleted"
	case "empty input":
		return "empty_input"
	case "empty summary":
		return "empty_summary"
	case "summary failed; no content fallback":
		return "summary_failed"
	case "missing exact entry identifier":
		return "missing_entry_id"
	case "capture persistence unavailable; payload not retained":
		return "payload_unavailable"
	default:
		return fallback
	}
}

// MemoryStatuses keeps historical status readable even after policy revocation.
// Only retry eligibility depends on current authorization.
func (s *Service) MemoryStatuses(ctx context.Context, user, chat string) ([]MemoryStatus, error) {
	if _, err := s.Get(ctx, user, chat); err != nil {
		return nil, err
	}
	records, err := s.d.Store.ListCaptures(ctx, s.Principal(user), chat)
	if err != nil {
		return nil, err
	}
	out := make([]MemoryStatus, 0, len(records))
	for _, r := range records {
		v, err := projectMemoryStatus(r)
		if err != nil {
			return nil, err
		}
		if v.Status == "failed" && !r.CaptureUnavailable {
			v.RetryEligible = s.AuthorizeCapture(ctx, r.Identity) == nil
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Service) RetryMemoryCapture(ctx context.Context, user, chat, id string) (MemoryStatus, error) {
	if _, err := s.Get(ctx, user, chat); err != nil {
		return MemoryStatus{}, err
	}
	p := s.Principal(user)
	records, err := s.d.Store.ListCaptures(ctx, p, chat)
	if err != nil {
		return MemoryStatus{}, err
	}
	var found *store.CaptureRecord
	for _, r := range records {
		if r.ID == id {
			found = &r
			break
		}
	}
	if found == nil {
		return MemoryStatus{}, store.ErrNoCapture
	}
	// Frozen routing is never a grant. Check before entering the store's atomic
	// failed-to-queued transition; the worker checks again before any write.
	if err := s.AuthorizeCapture(ctx, found.Identity); err != nil {
		return MemoryStatus{}, ErrMemoryDenied
	}
	if err := s.d.Store.RetryCapture(ctx, p, chat, id); err != nil {
		return MemoryStatus{}, err
	}
	s.publishMemoryCapture(*found)
	// A worker may already have advanced it. Return the current durable record.
	statuses, err := s.MemoryStatuses(ctx, user, chat)
	if err != nil {
		return MemoryStatus{}, err
	}
	for _, v := range statuses {
		if v.ID == id {
			return v, nil
		}
	}
	return MemoryStatus{}, store.ErrNoCapture
}

func (s *Service) publishMemoryCapture(r store.CaptureRecord) {
	if s.bus != nil {
		s.bus.Publish(key(r.Identity.Principal, r.Identity.Chat), session.Event{Kind: session.KindMemoryCapture, IDs: []string{r.ID}})
	}
}
