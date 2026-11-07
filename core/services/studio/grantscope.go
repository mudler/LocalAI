package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Builtin grants. An agent lists the grants the owner gave it (Builtins). What
// a chat of the agent holds is the intersection of that list with what the user
// may use at the turn, like the add-ons. A tool then exists in a chat when the
// grant is held, the chat did not switch its group off and the run's authority
// can have it (builtins.Chat.Holds).

// ErrUnknownGrant means an agent names a builtin grant this server does not
// declare.
var ErrUnknownGrant = errors.New("studio: unknown builtin grant")

// cleanGrants checks the grant names of an agent and removes repeats. With
// drop it silently leaves out a name the server does not know (an import from
// a newer server); otherwise it refuses the first one. A nil list stays nil.
func cleanGrants(names []string, drop bool) ([]string, error) {
	if names == nil {
		return nil, nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		switch {
		case !builtins.ValidGrant(n):
			if !drop {
				return nil, fmt.Errorf("%w: %q (GET /api/chats/agents/builtin-grants lists them)", ErrUnknownGrant, n)
			}
		case !slices.Contains(out, n):
			out = append(out, n)
		}
	}
	return out, nil
}

// grantUsable says whether the user may use a grant on this server now: the
// server offers the feature behind it and the user's account has it.
func (s *Service) grantUsable(ctx context.Context, user, grant string) bool {
	switch grant {
	case builtins.GrantMemoryWrite, builtins.GrantProfileWrite, builtins.GrantSkillsWrite, builtins.GrantPagesWrite, builtins.GrantDashboardWrite:
		if !s.d.Files.Available() || s.changes == nil {
			return false
		}
		return s.changes.grantsAKind(grant)
	case builtins.GrantKnowledgeWrite:
		return s.changes != nil && s.changes.grantsAKind(grant) && s.knowledgeAvailable(ctx, user)
	case builtins.GrantTriggersPropose, builtins.GrantTriggersRun:
		return s.triggers != nil && s.mayUseTriggers(ctx, user) == nil
	case builtins.GrantFilesUpload, builtins.GrantActFiles:
		return s.d.Files.Available()
	case builtins.GrantSavedAgents:
		return s.d.Agents != nil
	case builtins.GrantHistoryRead:
		return s.historyAvailable()
	}
	return builtins.ValidGrant(grant)
}

// usableGrants keeps the granted names the user may use, in the order of the
// declarations.
func (s *Service) usableGrants(ctx context.Context, user string, granted []string) []string {
	var out []string
	for _, g := range builtins.Grants() {
		if slices.Contains(granted, g.Name) && s.grantUsable(ctx, user, g.Name) {
			out = append(out, g.Name)
		}
	}
	return out
}

// AgentGrants returns the builtin grants that the saved agent holds now: what
// the owner granted, intersected with what the owner may use. A missing agent
// holds none. It is the answer for any gate that asks whether an agent has a
// right.
func (s *Service) AgentGrants(ctx context.Context, user, agentID string) []string {
	if s.d.Agents == nil || agentID == "" {
		return nil
	}
	ag, err := s.chatAgent(ctx, principal(user), agentID)
	if err != nil {
		return nil
	}
	return s.usableGrants(ctx, principal(user), ag.Builtins)
}

// chatGrants are the grants a chat of a saved agent holds, for the tools.
func (s *Service) chatGrants(ctx context.Context, rec session.ChatRecord) []string {
	if rec.AgentID == "" {
		return nil
	}
	if sc := s.scopeOf(ctx, rec); sc != nil {
		return sc.builtinNames
	}
	return nil
}

// BuiltinGrants lists the declared grants for the editor and the chat.
func (s *Service) BuiltinGrants() []builtins.Grant { return builtins.Grants() }

// checkChangeGrant refuses a proposal of an agent that does not hold the
// grant of its kind, or a run that cannot hold rights (one that started by
// itself). A chat without an agent is the user's own and is not asked.
func (s *Service) checkChangeGrant(ctx context.Context, user, agentID string, src contract.ChangeSource, grant string) error {
	if src.Kind != "" && src.Kind != session.OriginConnector {
		return fmt.Errorf("%w: a run that started by itself cannot change this", proposals.ErrNotGranted)
	}
	if agentID == "" {
		return nil
	}
	held := s.AgentGrants(ctx, user, agentID)
	if grant == "" {
		return nil
	}
	if !slices.Contains(held, grant) {
		return fmt.Errorf("%w (%s)", proposals.ErrNotGranted, grant)
	}
	return nil
}

// checkAnyChangeGrant is the check for a revert: any change grant will do,
// because an undo only restores an earlier text.
func (s *Service) checkAnyChangeGrant(ctx context.Context, user, agentID string, src contract.ChangeSource) error {
	if src.Kind != "" && src.Kind != session.OriginConnector {
		return fmt.Errorf("%w: a run that started by itself cannot change this", proposals.ErrNotGranted)
	}
	if agentID == "" {
		return nil
	}
	for _, g := range s.AgentGrants(ctx, user, agentID) {
		if s.changes.grantsAKind(g) {
			return nil
		}
	}
	return proposals.ErrNotGranted
}
