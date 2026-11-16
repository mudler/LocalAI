package studio

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// briefPromptRunes is the longest prompt a brief carries.
const briefPromptRunes = 200

// BindingBrief is a messaging connection of a project agent. It has no token
// and no list of senders.
type BindingBrief struct {
	ID, Kind, Name, AgentID, AgentName string
	Enabled                            bool
}

// TriggerBrief is a trigger of a project agent. Next is nil unless a schedule
// trigger is on and has a later time. The prompt is not part of it.
type TriggerBrief struct {
	ID, Name, Kind, AgentID, AgentName, Schedule, Folder string
	Enabled, Proposed                                    bool
	Next                                                 *time.Time
}

// LoopBrief is a loop in a chat of a project agent. Prompt is cut to a short
// text.
type LoopBrief struct {
	ID, ChatID, ChatTitle, Prompt string
	State                         string
}

func capPrompt(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > briefPromptRunes {
		return string(r[:briefPromptRunes-1]) + "…"
	}
	return s
}

// projectAgentNames resolves what the three listings share: the project must
// be the user's own (projects.ErrNotFound otherwise) and the result maps the ID
// of each agent of the project to its name. An archived project resolves as
// usual: the lists show what exists, and an archive stops the runs, not the
// rows.
func (s *Service) projectAgentNames(ctx context.Context, user, projectID string) (map[string]string, error) {
	if s.d.Projects == nil {
		return nil, projects.ErrNotFound
	}
	if _, err := s.d.Projects.Get(ctx, principal(user), projectID); err != nil {
		return nil, err
	}
	list, err := s.AgentsOfProject(ctx, user, projectID)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(list))
	for _, a := range list {
		names[a.ID] = a.Name
	}
	return names, nil
}

// ProjectConnections lists the messaging connections bound to the agents of a
// project, as the user sees them. A project of another user, or an unknown
// one, is projects.ErrNotFound. A server without connectors lists none. It
// reads the bindings only, not the triggers or the loops: each route of the
// project page computes its own list.
func (s *Service) ProjectConnections(ctx context.Context, user, projectID string) ([]BindingBrief, error) {
	out := []BindingBrief{}
	names, err := s.projectAgentNames(ctx, user, projectID)
	if err != nil || len(names) == 0 || s.connectors == nil {
		return out, err
	}
	all, err := s.connectors.o.Store.ListBindings(ctx, principal(user))
	if err != nil {
		return out, err
	}
	for _, b := range all {
		if name, ok := names[b.AgentID]; ok {
			out = append(out, BindingBrief{ID: b.ID, Kind: b.Kind, Name: b.Name, AgentID: b.AgentID, AgentName: name, Enabled: b.Enabled})
		}
	}
	return out, nil
}

// ProjectTriggers lists the triggers that run an agent of a project. A file
// trigger's folder is the path in the owner's home, as the trigger API shows
// it, never the internal absolute path. It reads the triggers only.
func (s *Service) ProjectTriggers(ctx context.Context, user, projectID string) ([]TriggerBrief, error) {
	out := []TriggerBrief{}
	names, err := s.projectAgentNames(ctx, user, projectID)
	if err != nil || len(names) == 0 || s.triggers == nil {
		return out, err
	}
	all, err := s.triggers.ListTriggers(ctx, user)
	if err != nil {
		return out, err
	}
	for _, t := range all {
		name, ok := names[t.AgentID]
		if !ok {
			continue
		}
		b := TriggerBrief{ID: t.ID, Name: t.Name, Kind: string(t.Kind), AgentID: t.AgentID, AgentName: name, Schedule: t.Schedule,
			Folder: s.triggers.RelFolder(t), Enabled: t.Enabled, Proposed: t.Proposed}
		if next, ok := s.triggers.NextRun(t); ok {
			b.Next = &next
		}
		out = append(out, b)
	}
	return out, nil
}

// ProjectLoops lists the loops in chats of the agents of a project that have
// not ended: a loop that ended (a chat keeps the last few) is history, not a
// part of what the project does now. It reads the loops and, once for each
// distinct chat of a loop that is left, that chat, never the chat history. A
// chat that is gone or cannot be read leaves its loops out; the others still
// list.
func (s *Service) ProjectLoops(ctx context.Context, user, projectID string) ([]LoopBrief, error) {
	out := []LoopBrief{}
	names, err := s.projectAgentNames(ctx, user, projectID)
	if err != nil || len(names) == 0 || s.loops == nil {
		return out, err
	}
	all, err := s.loops.o.Store.ListOwner(ctx, principal(user))
	if err != nil {
		return out, err
	}
	titles := map[string]string{}
	chats := map[string]bool{}
	for _, l := range all {
		if l.State == loops.StateEnded {
			continue
		}
		ok, seen := chats[l.ChatID]
		if !seen {
			rec, err := s.getChatEffective(ctx, principal(user), l.ChatID)
			switch {
			case err == nil:
				_, mine := names[rec.AgentID]
				ok = mine && rec.Kind != session.KindSubAgent
				if ok {
					titles[l.ChatID] = rec.Title
				}
			case errors.Is(err, session.ErrNotFound):
			default:
				xlog.Warn("studio: a chat of a loop could not be read", "chat", l.ChatID, "error", err)
			}
			chats[l.ChatID] = ok
		}
		if ok {
			out = append(out, LoopBrief{ID: l.ID, ChatID: l.ChatID, ChatTitle: titles[l.ChatID], Prompt: capPrompt(l.Prompt), State: string(l.State)})
		}
	}
	return out, nil
}
