// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

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
	return store.MemoryAcceptance{Principal: p, ChatID: conv, SourceID: "memory:" + newID(), Text: text,
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
		s.log.Warn("studio: could not persist incoming memory intent", "chat", conv, "error", err)
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
		s.log.Warn("studio: could not persist pending memory intent", "chat", conv, "error", err)
	}
	return s.d.Store.AddPending(ctx, p, conv, payload)
}

func (s *Service) completeMemoryRun(ctx context.Context, p, conv string, r *run, status store.TurnStatus) error {
	if len(r.memoryInputs) > 0 {
		err := s.d.Store.CompleteMemoryTurn(ctx, store.MemoryTurnCompletion{Principal: p, ChatID: conv, RunID: r.id, ReplyID: r.id, ReplyText: r.memoryReply, Status: status, IncomingIDs: r.memoryInputs})
		if err == nil {
			return nil
		}
		s.log.Warn("studio: could not persist completed memory intent", "chat", conv, "error", err)
	}
	return s.releaseTurn(ctx, p, conv, r.id, status)
}
