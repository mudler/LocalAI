// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/memory"
)

// Acceptance failure markers use an independent principal-scoped chat write.
// Completion fallback below retains the payload instead of creating a tombstone.
func failureMarker(d *captureData, a AcceptedInput, p, chat, source, kind string) error {
	i := memory.Identity{Principal: p, Chat: chat, Agent: a.Policy.AgentID, Destination: a.Policy.Destination, Source: source, Kind: kind}
	id := CaptureKey(i)
	if d.DeadKeys[id] {
		return nil
	}
	for _, old := range d.Records {
		if old.Identity.Source == source && old.Identity.Kind == kind {
			return nil
		}
	}
	r, err := project(CaptureRecord{ID: id, Identity: i, Origin: a.Origin, CaptureUnavailable: true}, memory.Failed{Job: memory.Job{Identity: i, Input: memory.ContentInput{}, Expires: time.Now().Add(24 * time.Hour).Unix()}, Reason: "capture persistence unavailable; payload not retained"}, "R07-capture-unavailable")
	if err == nil {
		d.Records[id] = r
	}
	return err
}
func acceptanceFailure(ctx context.Context, in MemoryAcceptance, edit captureEdit) error {
	if err := validateAcceptance(in); err != nil {
		return err
	}
	if in.Policy.Mode == "" || in.Policy.Mode == CaptureOff || strings.TrimSpace(in.Text) == "" {
		return nil
	}
	return edit(ctx, in.Principal, in.ChatID, func(d *captureData, c *chatState) error {
		if c.Rec.AgentID != in.Policy.AgentID {
			return ErrCaptureConflict
		}
		if _, ok := d.Accepted[in.SourceID]; ok {
			return nil
		}
		if d.DeadSources[in.SourceID] {
			return nil
		}
		kind := "incoming"
		if in.Policy.Mode == CaptureSummary {
			kind = "summary"
		}
		if err := failureMarker(d, AcceptedInput{Policy: in.Policy, Origin: in.Origin}, in.Principal, in.ChatID, in.SourceID, kind); err != nil {
			return err
		}
		// No acceptance payload is available. Replays cannot create a newly routed job.
		d.DeadSources[in.SourceID] = true
		return nil
	})
}

// Completion fallback bypasses the failed public operation, not its invariants.
// The ordinary chat boundary commits the outcome receipt, frozen capture payload
// and turn release together. Queued records then use normal worker recovery.
func completionFailure(ctx context.Context, in MemoryTurnCompletion, edit captureEdit) error {
	return completeMemory(ctx, in, edit)
}
func (s *jsonStore) RecordMemoryAcceptanceFailure(ctx context.Context, in MemoryAcceptance) error {
	return acceptanceFailure(ctx, in, s.editCapture)
}
func (s *gormStore) RecordMemoryAcceptanceFailure(ctx context.Context, in MemoryAcceptance) error {
	return acceptanceFailure(ctx, in, s.editCapture)
}
func (s *jsonStore) RecordMemoryCompletionFailure(ctx context.Context, in MemoryTurnCompletion) error {
	return completionFailure(ctx, in, s.editCapture)
}
func (s *gormStore) RecordMemoryCompletionFailure(ctx context.Context, in MemoryTurnCompletion) error {
	return completionFailure(ctx, in, s.editCapture)
}
