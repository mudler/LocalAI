package studio

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
)

// SaveAgent creates a saved agent of the user (a.ID empty) or replaces one (a.ID
// set; it must be the user's). The caller's Principal, Created and Updated are
// ignored. It returns the stored agent.
func (s *Service) SaveAgent(ctx context.Context, user string, a agents.Agent) (agents.Agent, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, ErrAgentsOff
	}
	p := principal(user)
	if err := agents.Validate(a); err != nil {
		return agents.Agent{}, err
	}
	a.Principal, a.Name = p, trimName(a.Name)
	now := time.Now().UTC()
	a.Updated = now
	if a.ID == "" {
		a.ID, a.Created = newID(), now
	} else if _, err := s.d.Agents.Get(ctx, p, a.ID); err != nil {
		return agents.Agent{}, err
	}
	if err := s.d.Agents.Put(ctx, a); err != nil {
		return agents.Agent{}, err
	}
	return s.d.Agents.Get(ctx, p, a.ID)
}

// Agent returns a saved agent of the user.
func (s *Service) Agent(ctx context.Context, user, id string) (agents.Agent, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, ErrAgentsOff
	}
	return s.d.Agents.Get(ctx, principal(user), id)
}

// Agents lists the saved agents of the user, sorted by name.
func (s *Service) Agents(ctx context.Context, user string) ([]agents.Agent, error) {
	if s.d.Agents == nil {
		return nil, ErrAgentsOff
	}
	return s.d.Agents.List(ctx, principal(user))
}

// DeleteAgent removes a saved agent. Chats that ran with it keep their record;
// their next turn runs without tools and says the agent was deleted.
func (s *Service) DeleteAgent(ctx context.Context, user, id string) error {
	if s.d.Agents == nil {
		return ErrAgentsOff
	}
	return s.d.Agents.Delete(ctx, principal(user), id)
}

func trimName(n string) string { return strings.TrimSpace(n) }
