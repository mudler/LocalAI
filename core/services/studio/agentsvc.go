package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
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

// ErrNoTemplate means no starter agent has this ID.
var ErrNoTemplate = errors.New("studio: no such agent template")

// ImportAgent saves an agent that came from a file as a new agent of the user.
// When the user already has an agent with the name, " (2)", " (3)" and so on
// is added to the name. The ID, owner and times of the file are not used.
func (s *Service) ImportAgent(ctx context.Context, user string, a agents.Agent) (agents.Agent, error) {
	a.ID = ""
	name := trimName(a.Name)
	for n := 1; n <= maxImportRenames; n++ {
		a.Name = name
		if n > 1 {
			a.Name = fmt.Sprintf("%s (%d)", name, n)
		}
		saved, err := s.SaveAgent(ctx, user, a)
		if !errors.Is(err, agents.ErrNameTaken) {
			return saved, err
		}
	}
	return agents.Agent{}, agents.ErrNameTaken
}

// maxImportRenames is how many names an import tries before it gives up.
const maxImportRenames = 20

// CreateAgentFromTemplate saves a starter agent as an agent of the user. A
// non-empty name replaces the template's.
func (s *Service) CreateAgentFromTemplate(ctx context.Context, user, id, name string) (agents.Agent, error) {
	t, ok := agents.TemplateByID(id)
	if !ok {
		return agents.Agent{}, ErrNoTemplate
	}
	if strings.TrimSpace(name) == "" {
		name = t.Name
	}
	return s.SaveAgent(ctx, user, agents.Agent{Name: name, Description: t.Description, Instructions: t.Instructions})
}

// AgentRun is a chat that ran with a saved agent: one the person had with it,
// one a trigger started, or one a connector started.
type AgentRun struct {
	Chat session.ChatRecord
	Turn store.TurnInfo
}

// AgentHistory is the runs of a saved agent, newest first, with the counts of
// all of them.
type AgentHistory struct {
	Runs                   []AgentRun
	Total, Failed, Running int
}

// maxHistoryScan is how many of the user's newest chats are searched for the
// runs of an agent.
const maxHistoryScan = 5000

// AgentRuns returns the chats of the user that ran with the agent, newest
// first, up to limit, and counts all that are in the newest maxHistoryScan.
func (s *Service) AgentRuns(ctx context.Context, user, id string, limit int) (AgentHistory, error) {
	if _, err := s.Agent(ctx, user, id); err != nil {
		return AgentHistory{}, err
	}
	p := principal(user)
	var (
		h      AgentHistory
		cursor session.ChatCursor
		seen   int
	)
	for seen < maxHistoryScan {
		page, err := s.d.Store.ListChats(ctx, p, cursor, 200)
		if err != nil {
			return AgentHistory{}, err
		}
		for _, rec := range page {
			if rec.AgentID != id || rec.Kind == session.KindSubAgent {
				continue
			}
			info, err := s.d.Store.Turn(ctx, p, rec.ID)
			if err != nil {
				continue // deleted meanwhile
			}
			h.Total++
			switch {
			case info.Running:
				h.Running++
			case info.Last == store.TurnFailed || info.Last == store.TurnInterrupted:
				h.Failed++
			}
			if limit <= 0 || len(h.Runs) < limit {
				h.Runs = append(h.Runs, AgentRun{Chat: rec, Turn: info})
			}
		}
		seen += len(page)
		if len(page) < 200 {
			break
		}
		last := page[len(page)-1]
		cursor = session.ChatCursor{Updated: last.Updated, ID: last.ID}
	}
	return h, nil
}
