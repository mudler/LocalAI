package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The composer of a new chat shows the Tools and Skills menus before the chat
// exists. It lists what a chat on the chosen model (and saved agent) could
// switch, keeps the person's choices as a draft and sends them with the
// create request (CreateInput.Switches), so the chat is stored with them.

// DraftTools lists what a new chat on model, with the saved agent agentID when
// it is set, could use, every switch in its default state. The agent's model,
// when it has one, replaces model, as Create does.
func (s *Service) DraftTools(ctx context.Context, user, model, agentID string) (ToolsPanel, error) {
	rec, err := s.draftRecord(ctx, user, model, agentID)
	if err != nil {
		return ToolsPanel{}, err
	}
	return s.toolsFor(ctx, user, rec)
}

// DraftAddons lists the add-ons of a new chat as DraftTools does, and whether
// it could run commands at its first turn.
func (s *Service) DraftAddons(ctx context.Context, user, model, agentID string) ([]AddonInfo, Commands, error) {
	rec, err := s.draftRecord(ctx, user, model, agentID)
	if err != nil {
		return nil, Commands{}, err
	}
	list, err := s.addonsFor(ctx, user, rec)
	if err != nil {
		return nil, Commands{}, err
	}
	return list, s.commandsFor(ctx, principal(user), rec), nil
}

// draftRecord is the record a new chat would have, with no ID and no switch.
func (s *Service) draftRecord(ctx context.Context, user, model, agentID string) (session.ChatRecord, error) {
	rec := session.ChatRecord{Principal: principal(user), Kind: session.KindChat, Model: model, AgentID: agentID}
	if agentID == "" {
		return rec, nil
	}
	if s.d.Agents == nil {
		return rec, ErrAgentsOff
	}
	ag, err := s.d.Agents.Get(ctx, principal(user), agentID)
	if err != nil {
		return rec, err
	}
	if ag.Model != "" {
		rec.Model = ag.Model
	}
	return rec, nil
}
