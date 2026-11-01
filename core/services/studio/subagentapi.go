package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// SubAgent is a sub-agent run of a conversation and the state of its turn.
type SubAgent struct {
	Chat session.ChatRecord
	Turn store.TurnInfo
}

// SubAgentLimit is how many sub-agents one conversation may run in the
// background at once.
func (s *Service) SubAgentLimit() int { return maxBackgroundChildren }

// SubAgents lists the sub-agent runs of a conversation, oldest first, with
// their state. It reads the store, so it shows runs started on any frontend.
func (s *Service) SubAgents(ctx context.Context, user, parent string) ([]SubAgent, error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, parent); err != nil {
		return nil, err
	}
	kids, err := s.d.Store.ListChildren(ctx, p, parent)
	if err != nil {
		return nil, err
	}
	out := make([]SubAgent, 0, len(kids))
	for _, k := range kids {
		info, err := s.d.Store.Turn(ctx, p, k.ID)
		if err != nil {
			continue // deleted meanwhile
		}
		out = append(out, SubAgent{Chat: k, Turn: info})
	}
	return out, nil
}

// subAgent returns the child if it is a sub-agent run of parent, else ErrNotFound.
func (s *Service) subAgent(ctx context.Context, p, parent, child string) (session.ChatRecord, error) {
	rec, err := s.d.Store.GetChat(ctx, p, child)
	if err != nil {
		return session.ChatRecord{}, err
	}
	if rec.Kind != session.KindSubAgent || rec.ParentID != parent {
		return session.ChatRecord{}, session.ErrNotFound
	}
	return rec, nil
}

// SubAgentMessages reads the transcript of a sub-agent run of a conversation.
func (s *Service) SubAgentMessages(ctx context.Context, user, parent, child string, after int64, limit int) ([]session.Entry, error) {
	p := principal(user)
	if _, err := s.subAgent(ctx, p, parent, child); err != nil {
		return nil, err
	}
	return s.d.Store.Entries(ctx, child, after, limit)
}

// StopSubAgent ends the running turn of a sub-agent run. The parent goes on;
// the stopped run reaches it as a failed sub-agent.
func (s *Service) StopSubAgent(ctx context.Context, user, parent, child string) error {
	p := principal(user)
	if _, err := s.subAgent(ctx, p, parent, child); err != nil {
		return err
	}
	return s.Stop(ctx, p, child)
}
