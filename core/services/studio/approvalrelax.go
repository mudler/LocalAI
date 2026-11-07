package studio

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Who may run an add-on tool without a confirmation.
//
// A tool of an add-on can declare approval "always", and a person can relax
// that for one tool with their own setting (see contract.ApprovalOverridable).
// The setting is the person's, so it is honored only for the person: a chat
// the person has open themselves, or a messaging chat in which every sender of
// the turn is paired with the bot. A sender who is only on the allowlist, a
// sender let in because the binding accepts anyone, a role, a trigger, a job,
// a page and a sub-agent are not the person, and their calls keep asking.
// Doubt is no: a chat that cannot be read, that belongs to another person or
// whose senders are unknown asks.

// ApprovalRelaxed says whether the person's own setting lets the tool of the
// add-on run without a confirmation in the chat chatID. Both must hold: the
// person chose "skip" for this tool (the setting is read for the owner of the
// chat, never from an argument) and the chat qualifies.
func (s *Service) ApprovalRelaxed(ctx context.Context, user, chatID, addonID, tool string) bool {
	if s.d.Store == nil || s.d.UserSetting == nil || chatID == "" {
		return false
	}
	v, found, err := s.d.UserSetting(ctx, principal(user), addonID, contract.ApprovalSettingKey(tool))
	if err != nil || !found || v != contract.ApprovalSkip {
		return false
	}
	rec, err := s.d.Store.GetChat(ctx, principal(user), chatID)
	if err != nil {
		if !errors.Is(err, session.ErrNotFound) {
			s.log.Warn("studio: the chat could not be read, the add-on tool will ask for a confirmation", "error", err)
		}
		return false
	}
	_, paired := s.messagingFacts(chatID)
	return chatMayRelaxApproval(rec, principal(user), paired)
}

// chatMayRelaxApproval is the rule of ApprovalMayRelax on the facts it reads.
// paired is true when every sender of the running turn is paired with the bot.
func chatMayRelaxApproval(rec session.ChatRecord, owner string, paired bool) bool {
	if rec.Principal != owner || (rec.Kind != "" && rec.Kind != session.KindChat) {
		return false
	}
	switch rec.Origin {
	case "":
		return true
	case session.OriginConnector:
		return paired
	}
	return false
}

// RecordRelaxedRun adds a run without a confirmation to the recent decisions
// of the connection that acted, which its page lists. The tool and the outcome
// are in it; the arguments never are. The chat, when a messaging chat, gives
// the sender IDs.
func (s *Service) RecordRelaxedRun(binding, chatID, tool, outcome string) {
	var senders []string
	if g, ok := s.access.get(chatID); ok {
		senders = g.Senders
	}
	s.access.append(AccessEvent{At: s.now().UTC(), Binding: binding, Senders: senders, Capability: contract.CapToolsWrite, Allowed: true,
		Via: "no-confirmation", Reason: tool + ": " + outcome})
}

// approvalsOf lists the tools of the add-on that serves the binding whose
// confirmation the owner can relax, with the owner's setting and who it
// applies to now. A doubt shows the tool as asking.
func (s *Service) approvalsOf(ctx context.Context, b connectors.Binding) []ToolApproval {
	if s.d.AddonTools == nil || b.AddonID == "" {
		return nil
	}
	tools, err := s.d.AddonTools.Tools(ctx, b.Owner)
	if err != nil {
		return nil
	}
	var out []ToolApproval
	seen := map[string]bool{}
	for _, t := range tools {
		if t.AddonID != b.AddonID || !t.ApprovalOverridable || t.Tool == "" || seen[t.Tool] {
			continue
		}
		seen[t.Tool] = true
		a := ToolApproval{Tool: t.Tool}
		if s.d.UserSetting != nil {
			v, found, err := s.d.UserSetting(ctx, b.Owner, b.AddonID, contract.ApprovalSettingKey(t.Tool))
			a.Skip = err == nil && found && v == contract.ApprovalSkip
		}
		if a.Skip && b.Writes() != connectors.WriteOff {
			a.RunsWithoutAsking = []string{"owner", "paired"}
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y ToolApproval) int { return strings.Compare(x.Tool, y.Tool) })
	return out
}
