package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// A chat may call the user's saved agents as sub-agents: list_agents names
// them, and spawn_agent with agent starts one. Both need the opt-in switch
// group builtins.GroupSavedAgents, which a chat has off until the person
// switches it on, and the agents feature of the user, as the agents API does.
// Everything is read from the store at each turn and each call, so every
// frontend of a cluster gives the same answer.

// listAgentsName is the name of the built-in tool that lists the saved agents.
const listAgentsName = "list_agents"

// listAgentsMax is the most agents list_agents returns.
const listAgentsMax = 100

// Errors of the saved agents in a chat, returned to the model as the result
// of the tool call.
var (
	// ErrSavedAgentsOff means an administrator switched the saved agents of
	// the chats off (chats_saved_agents), or the server keeps none.
	ErrSavedAgentsOff = errors.New("calling saved agents from a chat is switched off on this server")
	// ErrSavedAgentsNotAllowed means the chat did not switch the saved agents
	// on.
	ErrSavedAgentsNotAllowed = errors.New("this chat does not allow calling saved agents; the person can switch \"Saved agents\" on in the chat's Tools menu")
	// ErrSavedAgentsForbidden means the user may not use saved agents.
	ErrSavedAgentsForbidden = errors.New("saved agents are not enabled for this account")
	// ErrSavedAgentWithTools means spawn_agent named an agent and tools: the
	// agent's definition decides its tools.
	ErrSavedAgentWithTools = errors.New("tools cannot be combined with agent: the saved agent's definition decides its tools")
	// ErrSavedAgentUnattended means a run that nobody watches (a trigger or a
	// job) tried to start a saved agent: that agent would not carry the limits
	// of the run.
	ErrSavedAgentUnattended = errors.New("a run that nobody watches cannot start a saved agent")
)

const listAgentsDescription = "List the saved agents of the user that you may start as sub-agents: id, name and description, and the folder and mode (read or write) of an agent that works in one. " +
	"When you work in a folder, only agents that work in that folder, inside it or in no folder are listed. " +
	"Start one with spawn_agent and its id (or its name) in agent; it then works with its own instructions, model and tools."

// registerSavedAgents adds list_agents to the registry of built-in tools. It is
// opt-in per chat and never reaches a sub-agent: sub-agents nest one level.
func (s *Service) registerSavedAgents() error {
	if s.d.Agents == nil {
		return nil
	}
	return s.builtins.Register(builtins.Tool{
		ID: listAgentsName, Group: builtins.GroupSavedAgents, OptIn: true, Label: "Saved agents",
		Description: "Let a chat list the user's saved agents and start one as a sub-agent. Each chat must switch it on in its Tools menu.",
		Setting:     builtins.SettingSavedAgents,
		ReadOnly:    true,
		// Read at each turn, so a change of the setting applies at the next
		// turn of every chat.
		Enabled: s.savedAgentsOn,
		Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
			if c.SubAgent {
				return session.Tool{}, false, "a sub-agent cannot start sub-agents"
			}
			if slices.Contains(c.GroupsOff, builtins.GroupSubAgents) {
				return session.Tool{}, false, "sub-agents are off for this chat"
			}
			p := c.Actor.Principal
			if err := s.mayUseAgents(ctx, p); err != nil {
				return session.Tool{}, false, err.Error()
			}
			return session.Tool{Description: listAgentsDescription, Schema: json.RawMessage(`{"type":"object","properties":{}}`),
				// The user comes from the chat, never from the arguments.
				Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
					return s.listAgentsTool(ctx, p, c.Actor.ChatID)
				}}, true, ""
		},
	})
}

// savedAgentsOn reports whether an administrator left the saved agents of the
// chats on.
func (s *Service) savedAgentsOn() bool {
	return s.d.SavedAgentsOn == nil || s.d.SavedAgentsOn()
}

// mayUseAgents checks that the user may use saved agents now: the store
// exists and, with accounts, the user has the agents feature. It is the check
// of the agents API, made again at each call because a chat can outlive a
// permission.
func (s *Service) mayUseAgents(ctx context.Context, p string) error {
	if s.d.Agents == nil {
		return ErrSavedAgentsOff
	}
	if s.d.Users == nil {
		return nil
	}
	u, err := s.d.Users(ctx, p)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSavedAgentsForbidden
	}
	if s.d.Allowed != nil && !s.d.Allowed(u, auth.FeatureAgents) {
		return ErrSavedAgentsForbidden
	}
	return nil
}

// savedAgentsAllowed checks that a chat may call saved agents now: the
// setting is on, the chat switched the group on, and its user may use agents.
func (s *Service) savedAgentsAllowed(ctx context.Context, rec session.ChatRecord) error {
	t, ok := s.builtins.Lookup(listAgentsName)
	if !ok || !t.On() {
		return ErrSavedAgentsOff
	}
	if !slices.Contains(rec.BuiltinsOn, builtins.GroupSavedAgents) || slices.Contains(rec.BuiltinsOff, builtins.GroupSavedAgents) {
		return ErrSavedAgentsNotAllowed
	}
	if slices.Contains(rec.BuiltinsOff, builtins.GroupSubAgents) {
		return ErrSubAgentsOff
	}
	return s.mayUseAgents(ctx, rec.Principal)
}

type listedAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	// Folder is the name of the folder the agent works in and FolderMode is
	// "read" or "write"; both are empty for an agent without a folder. The
	// path is not given: it would show the folders above the chat's own.
	Folder     string `json:"folder,omitempty"`
	FolderMode string `json:"folder_mode,omitempty"`
	// FolderCount is how many folders the agent has, when it has more than
	// one; Folder and FolderMode then describe the first.
	FolderCount int `json:"folder_count,omitempty"`
	// FolderMissing is set when the agent has a folder that is deleted or not
	// chosen: it cannot run until the person fixes it.
	FolderMissing bool `json:"folder_missing,omitempty"`
}

// listAgentsTool is list_agents: the user's own agents, never another's. For
// a chat whose agent has a folder it lists only the agents spawn_agent would
// start there (agentFitsScope), so the model is not offered one it cannot use.
func (s *Service) listAgentsTool(ctx context.Context, p, chatID string) (string, error) {
	if err := s.mayUseAgents(ctx, p); err != nil {
		return "", err
	}
	chat, err := s.d.Store.GetChat(ctx, p, chatID)
	if err != nil {
		return "", err
	}
	outer, err := s.agentFolderScope(ctx, chat, false)
	if err != nil {
		return "", err
	}
	list, err := s.d.Agents.List(ctx, p)
	if err != nil {
		return "", fmt.Errorf("could not list the saved agents: %w", err)
	}
	out := struct {
		Agents    []listedAgent `json:"agents"`
		Truncated bool          `json:"truncated,omitempty"`
	}{Agents: []listedAgent{}}
	for _, a := range list {
		if !s.agentFitsScope(ctx, p, outer, a) {
			continue
		}
		if len(out.Agents) == listAgentsMax {
			out.Truncated = true
			break
		}
		l := listedAgent{ID: a.ID, Name: a.Name, Description: a.Description, Model: a.Model}
		if a.HasFolder() {
			l.FolderMode = a.FolderList()[0].Mode
			l.FolderCount = len(a.FolderList())
			if sc, err := s.liveFolder(ctx, p, a); err != nil {
				l.FolderMissing = true
			} else {
				l.Folder = path.Base(sc.primary().path)
			}
		}
		out.Agents = append(out.Agents, l)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// resolveAgent finds a saved agent of the user by its ID, or else by its name
// (ignoring case). Another user's agent is never found.
func (s *Service) resolveAgent(ctx context.Context, p, ref string) (agents.Agent, error) {
	ref = strings.TrimSpace(ref)
	ag, err := s.d.Agents.Get(ctx, p, ref)
	if err == nil {
		return ag, nil
	}
	if !errors.Is(err, agents.ErrNotFound) {
		return agents.Agent{}, fmt.Errorf("could not read the saved agent: %w", err)
	}
	list, err := s.d.Agents.List(ctx, p)
	if err != nil {
		return agents.Agent{}, fmt.Errorf("could not list the saved agents: %w", err)
	}
	for _, a := range list {
		if agents.NameKey(a.Name) == agents.NameKey(ref) {
			return a, nil
		}
	}
	return agents.Agent{}, fmt.Errorf("no saved agent %q: call %s for the agents you may start", ref, listAgentsName)
}
