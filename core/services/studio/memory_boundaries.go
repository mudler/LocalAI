// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

type memoryDeliveryKey struct{}

func withMemoryDelivery(ctx context.Context, id string) context.Context {
	if id == "" {
		id, _ = ctx.Value(memoryDeliveryKey{}).(string)
		if id == "" {
			id = newID()
		}
	}
	return context.WithValue(ctx, memoryDeliveryKey{}, id)
}

// Acceptance freezes routing, not authorization. The worker reloads policy and
// principal access immediately before mutation. Plain chats never opt in.
func (s *Service) memoryAcceptance(ctx context.Context, p, conv, text string) (store.MemoryAcceptance, bool) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || rec.AgentID == "" || s.d.Agents == nil {
		return store.MemoryAcceptance{}, false
	}
	ag, err := s.d.Agents.Get(ctx, p, rec.AgentID)
	if err != nil {
		return store.MemoryAcceptance{}, false
	}
	ag, err = agents.NormalizeMemory(ag)
	if err != nil || ag.Memory.AutomaticIngestion == "off" {
		return store.MemoryAcceptance{}, false
	}
	id, _ := ctx.Value(memoryDeliveryKey{}).(string)
	if id == "" {
		id = newID()
	}
	return store.MemoryAcceptance{Principal: p, ChatID: conv, SourceID: "memory:" + id, Text: text,
		Policy: store.CapturePolicy{AgentID: ag.ID, Destination: ag.Memory.Destination, Mode: store.CaptureMode(ag.Memory.AutomaticIngestion)},
		Origin: store.CaptureOrigin{Kind: rec.Origin, ConnectorID: rec.OriginID}}, true
}

func (s *Service) acceptDirectMemory(ctx context.Context, p, conv, text string) string {
	in, ok := s.memoryAcceptance(ctx, p, conv, text)
	if !ok {
		return ""
	}
	in.Delivered = true
	accepted, err := s.d.Store.AcceptMemoryInput(ctx, in)
	if err != nil {
		s.recordAcceptanceFailure(ctx, in)
		return ""
	}
	return accepted.ID
}

func (s *Service) acceptPendingMemory(ctx context.Context, p, conv, text, payload string) (store.Pending, error) {
	if in, ok := s.memoryAcceptance(ctx, p, conv, text); ok {
		in.PendingText = payload
		accepted, err := s.d.Store.AcceptMemoryInput(ctx, in)
		if err == nil {
			return accepted.Pending, nil
		}
		// Memory must not prevent an answer or connector delivery. The ordinary
		// queue remains available even when capture persistence is unavailable.
		s.recordAcceptanceFailure(ctx, in)
	}
	return s.d.Store.AddPending(ctx, p, conv, payload)
}

func (s *Service) completeMemoryRun(ctx context.Context, p, conv string, r *run, status store.TurnStatus) error {
	if len(r.memoryInputs) > 0 {
		in := store.MemoryTurnCompletion{Principal: p, ChatID: conv, RunID: r.id, ReplyID: r.id, ReplyText: r.memoryReply, Status: status, IncomingIDs: r.memoryInputs}
		err := s.d.Store.CompleteMemoryTurn(ctx, in)
		if err == nil {
			return nil
		}
		// One independent chat write, with no retry loop in the answer path.
		// It releases the turn only with its durable outcome and capture intent.
		if handoffErr := s.d.Store.RecordMemoryCompletionFailure(ctx, in); handoffErr != nil {
			s.log.Warn("studio: memory completion handoff unavailable", "chat", conv)
			return handoffErr
		}
		return nil
	}
	return s.releaseTurn(ctx, p, conv, r.id, status)
}

// Bounded best effort: if the chat database itself is unavailable, a failure
// marker cannot be persisted either. Never retry in the answer path or log bodies.
func (s *Service) recordAcceptanceFailure(ctx context.Context, in store.MemoryAcceptance) {
	if err := s.d.Store.RecordMemoryAcceptanceFailure(ctx, in); err != nil {
		s.log.Warn("studio: memory failure status unavailable", "chat", in.ChatID)
	}
}
