package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/connsets"
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
	// The add-on gate reads the execution status. A draft has no chat, so the
	// scope has no conversation and the status says "draft".
	ctx, closeScope := s.executionScope(ctx, principal(user), rec.ID)
	defer closeScope()
	return s.toolsFor(ctx, user, rec)
}

// DraftAddons lists the add-ons of a new chat as DraftTools does, and whether
// it could run commands at its first turn. sw are the switches the person made
// in the composer so far (only the add-ons, the built-in groups and the opt-in
// groups count): the chat would start with them, so the commands state and the
// add-on switches answer for them, for example "switched off" when the
// sandbox group is off. They are checked as Create checks them.
func (s *Service) DraftAddons(ctx context.Context, user, model, agentID string, sw Switches) ([]AddonInfo, Commands, error) {
	rec, err := s.draftRecord(ctx, user, model, agentID)
	if err != nil {
		return nil, Commands{}, err
	}
	clean, err := s.cleanSwitches(Switches{AddonsOff: sw.AddonsOff, BuiltinsOff: sw.BuiltinsOff, BuiltinsOn: sw.BuiltinsOn})
	if err != nil {
		return nil, Commands{}, err
	}
	rec.AddonsOff = orNil(clean.AddonsOff)
	rec.BuiltinsOff = orNil(clean.BuiltinsOff)
	rec.BuiltinsOn = mergeGroups(rec.BuiltinsOn, clean.BuiltinsOn)
	rec.Connections = s.knownConnections(ctx, user, sw.Connections)
	ctx, closeScope := s.executionScope(ctx, principal(user), rec.ID)
	defer closeScope()
	list, err := s.addonsFor(ctx, user, rec)
	if err != nil {
		return nil, Commands{}, err
	}
	commands, err := s.commandsFor(ctx, principal(user), rec)
	if err != nil {
		return nil, Commands{}, err
	}
	return list, commands, nil
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
	// The opt-in groups the agent starts with are on in the draft.
	rec.BuiltinsOn = mergeGroups(rec.BuiltinsOn, ag.BuiltinsOn)
	return rec, nil
}

// knownConnections keeps the entries of a draft's connection sets that name
// connections the user has now. A draft that outlived a deleted connection
// then shows none selected instead of failing.
func (s *Service) knownConnections(ctx context.Context, user string, m connsets.Sets) connsets.Sets {
	var out connsets.Sets
	for addon, ids := range m {
		var known []string
		for _, id := range connsets.Dedup(ids) {
			if s.checkConnections(ctx, user, connsets.Sets{addon: {id}}) == nil {
				known = append(known, id)
			}
		}
		if len(known) > 0 {
			if out == nil {
				out = connsets.Sets{}
			}
			out[addon] = known
		}
	}
	return out
}
