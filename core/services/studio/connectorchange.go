package studio

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A change proposal that a connector chat made (a note, a profile, a skill, a
// page, the dashboard) is shown in that chat by the host, and the sender
// answers it there with a short word, exactly as for a trigger. The host reads
// the answer before any model turn, so the text never reaches the model.

const (
	// changeExcerptRunes bounds the text of a proposal that is written to a chat.
	changeExcerptRunes = 500
	// changeExcerptLines bounds the lines of it.
	changeExcerptLines = 8
)

// ChangeCode is the short code of a change proposal, the same on every
// frontend because it is derived from the ID of the proposal.
func ChangeCode(id string) string {
	sum := sha256.Sum256([]byte(id))
	var b [codeLen]byte
	for i := range b {
		b[i] = codeAlphabet[int(sum[i])%len(codeAlphabet)]
	}
	return string(b[:])
}

// kindWords says what a kind of change is, for a person who reads it in a chat.
func kindWords(v hostmsg.Voice, k contract.ChangeKind) string {
	switch k {
	case contract.ChangeMemory:
		return v.T("change.kind.memory")
	case contract.ChangeProfile:
		return v.T("change.kind.profile")
	case contract.ChangeSkill:
		return v.T("change.kind.skill")
	case contract.ChangePage:
		return v.T("change.kind.page")
	case contract.ChangeDashboard:
		return v.T("change.kind.dashboard")
	case contract.ChangeKnowledge:
		return v.T("change.kind.knowledge")
	}
	return v.T("change.kind.other")
}

// scopeWords says where a change applies.
func scopeWords(v hostmsg.Voice, s contract.ChangeScope) string {
	switch s {
	case contract.ScopeAgent:
		return v.T("change.scope.agent")
	case contract.ScopeChannel:
		return v.T("change.scope.channel")
	}
	return v.T("change.scope.all")
}

// addedLines returns the non-empty lines of next that cur does not have, in
// order. For a text that only removes lines it is empty.
func addedLines(cur, next string) []string {
	have := map[string]bool{}
	for _, l := range strings.Split(cur, "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var out []string
	for _, l := range strings.Split(next, "\n") {
		if t := strings.TrimSpace(l); t != "" && !have[t] {
			out = append(out, t)
		}
	}
	return out
}

// excerptOf shows the lines, at most changeExcerptLines and changeExcerptRunes.
func excerptOf(lines []string) string {
	more := false
	if len(lines) > changeExcerptLines {
		lines, more = lines[:changeExcerptLines], true
	}
	s := "+ " + strings.Join(lines, "\n+ ")
	if utf8.RuneCountInString(s) > changeExcerptRunes {
		s, more = string([]rune(s)[:changeExcerptRunes]), true
	}
	if more {
		s += "…"
	}
	return s
}

// proposalChatText is what the host writes in the chat where a change was
// proposed: what will be saved, and how to answer. The model's words are
// quoted, never trusted: mentions that notify everyone are broken.
func (h *Changes) proposalChatText(ctx context.Context, v hostmsg.Voice, p contract.ChangeProposal) string {
	var shown string
	if kind, ok := h.kinds.Lookup(string(p.Kind)); ok {
		switch p.Kind {
		case contract.ChangePage:
			shown = v.T("change.page_files")
		case contract.ChangeKnowledge:
			// The text is new to the collection: show its first lines.
			if lines := addedLines("", p.Content); len(lines) > 0 {
				shown = excerptOf(lines)
			}
		default:
			if st, err := h.snapshot(ctx, p.Owner, kind, p.Target, p.Content); err == nil {
				if add := addedLines(st.current, p.Content); len(add) > 0 {
					shown = excerptOf(add)
				} else {
					shown = v.T("change.only_removes")
				}
			}
		}
	}
	var b strings.Builder
	if p.Kind == contract.ChangeKnowledge {
		b.WriteString(v.T("change.propose_knowledge", "collection", p.Target, "summary", p.Summary))
	} else {
		b.WriteString(v.T("change.propose", "kind", kindWords(v, p.Kind), "scope", scopeWords(v, p.Scope), "summary", p.Summary))
	}
	if shown != "" {
		b.WriteString("\n" + shown)
	}
	code := ChangeCode(p.ID)
	b.WriteString("\n" + v.T("change.ask", "code", code))
	return neutralMentions.ReplaceAllString(b.String(), "${1}​${2}")
}

// connectorSender is the one sender whose message made the turn of the chat, or
// "" when several did or none is known.
func (h *Changes) connectorSender(chat string) string {
	if g, ok := h.s.access.get(chat); ok && len(g.Senders) == 1 {
		return g.Senders[0]
	}
	return ""
}

// sourceOf says where a change of the chat c comes from. A connector chat also
// names the sender, so that only that sender can answer in a shared chat.
func (h *Changes) sourceOf(c builtins.Chat) contract.ChangeSource {
	src := sourceOf(c)
	if c.Origin == session.OriginConnector {
		src.Sender = h.connectorSender(c.Actor.ChatID)
	}
	return src
}

// senderMayConfirm says whether the sender who made a proposal could also
// confirm it. A change of a kind that changes what the agent may do needs a
// paired sender, as the gate decides.
func (h *Changes) senderMayConfirm(ctx context.Context, c builtins.Chat, p contract.ChangeProposal) bool {
	if ok, _ := c.Holds(CapProposalsConfirm); !ok || p.Source.Sender == "" || p.Warning != "" {
		return false
	}
	if p.Kind == contract.ChangeMemory {
		// A note for the agent or the person, and any note under Always
		// confirm, is confirmed by a paired sender only.
		_, paired := h.s.messagingFacts(c.Actor.ChatID)
		return paired || !h.pairedAnswerOnly(ctx, p)
	}
	return h.s.allowCap(ctx, CapabilityRequest{Capability: CapProposalsConfirm, Binding: c.OriginID, Owner: p.Owner, Agent: c.AgentID,
		Sender: p.Source.Sender, Kind: string(p.Kind), Sensitive: true})
}

// pairedAnswerOnly says whether only a paired sender may confirm the memory
// note p: a note that reaches the agent's own notes or the person's (not the
// conversation's), or any note while the level in force is Always confirm.
func (h *Changes) pairedAnswerOnly(ctx context.Context, p contract.ChangeProposal) bool {
	return p.Kind == contract.ChangeMemory && (p.Scope != contract.ScopeChannel || h.alwaysConfirm(ctx, p))
}

// alwaysConfirm says whether the person's level or the agent's rule for the
// proposal is Always confirm. The messaging row cannot loosen it.
func (h *Changes) alwaysConfirm(ctx context.Context, p contract.ChangeProposal) bool {
	in := SaveInputs{User: h.storedSaveMode(ctx, string(p.Owner)), Agent: h.s.agentMemorySave(ctx, string(p.Owner), p.Agent),
		Messaging: MessagingAsk, Origin: session.OriginConnector}
	return effectiveSaveLevel(in) == SaveAlways
}

// connectorProposed writes the proposal in the chat of the sender and words the
// result for the model. The model is told what the host did, so it never sends
// the person to Studio when they can answer where they are.
func (h *Changes) connectorProposed(ctx context.Context, c builtins.Chat, p contract.ChangeProposal, autoNote string) string {
	head := fmt.Sprintf("Proposal %s stored: %s (%s, %s). Nothing is saved yet.", p.ID, p.Summary, p.Kind, p.Scope)
	conns := h.s.connectors
	if p.Warning != "" {
		// The host wrote nothing in the chat: a flagged text is never shown
		// there to be answered, and the answer would not be taken.
		return head + " The text looks like it holds a secret, so it cannot be confirmed from a chat: it waits for the owner in Studio, with a warning, and expires in 24 hours. " +
			"Tell them it is not saved and that the owner decides. Do not repeat the secret and do not say it is saved." + autoNote
	}
	if !h.senderMayConfirm(ctx, c, p) {
		return head + " The person cannot confirm changes from this chat, so it waits for the owner in Studio and expires in 24 hours. " +
			"Tell them it is not saved and that the owner decides. Do not say it is saved." + autoNote
	}
	chat, thread, ok := "", "", false
	if conns != nil {
		chat, thread, ok = conns.PlaceOf(ctx, c.OriginID, c.Actor.ChatID)
	}
	if ok {
		v := conns.voiceOf(ctx, c.OriginID, p.Source.Sender, chat)
		if _, err := conns.Post(ctx, Post{Binding: c.OriginID, Chat: chat, Thread: thread, Text: h.proposalChatText(ctx, v, p)}); err == nil {
			return head + " The host wrote the proposal in this chat, with what will be saved; the person answers there with yes or no, and it expires in 24 hours. " +
				"Say in one short sentence that you asked; do not repeat the text and do not say it is saved." + autoNote
		} else {
			h.s.log.Warn("studio: could not write a change proposal to its chat", "proposal", p.ID, "error", err)
		}
	}
	return head + " The host could not write the proposal in this chat, so it waits for the owner in Studio and expires in 24 hours. " +
		"Tell the person it is not saved yet. Do not say it is saved." + autoNote
}

// hereChanges lists the proposals of the binding that were made in the place
// where m is written, newest first, in every state. Where a chat is written is
// the platform chat and thread of its last message, as for a trigger.
func (c *Connectors) hereChanges(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) []contract.ChangeProposal {
	ch := c.svc.changes
	if ch == nil {
		return nil
	}
	all, err := ch.List(ctx, b.Owner)
	if err != nil {
		return nil
	}
	var out []contract.ChangeProposal
	for _, p := range all {
		if p.Source.Kind != "connector" || p.Source.ID != b.ID || p.Source.Chat == "" {
			continue
		}
		if chat, thread, ok := c.PlaceOf(ctx, b.ID, p.Source.Chat); !ok || chat != m.Chat || thread != m.Thread {
			continue
		}
		out = append(out, p)
	}
	return out
}

// mayAnswerChange asks the capability gate whether the sender may answer the
// proposal. A kind that changes what the agent may do is sensitive: only a
// paired sender passes.
func (c *Connectors) mayAnswerChange(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, p contract.ChangeProposal) bool {
	if c.svc.changes.pairedAnswerOnly(ctx, p) && !b.IsPaired(m.SenderID, c.o.Clock()) {
		return false
	}
	return c.svc.allowCap(ctx, CapabilityRequest{Capability: CapProposalsConfirm, Binding: b.ID, Owner: b.Owner, Agent: b.AgentID,
		Sender: m.SenderID, Username: m.SenderUsername, Roles: m.Roles, Chat: m.Chat, Direct: m.Direct, Kind: string(p.Kind), Sensitive: p.Kind != contract.ChangeMemory})
}

// answerChange confirms or drops the waiting proposal p for the sender of m. A
// sender who may not gets one short sentence; the proposal keeps waiting.
func (c *Connectors) answerChange(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, p contract.ChangeProposal, verb string, bare bool, v hostmsg.Voice) (string, bool) {
	ch := c.svc.changes
	detail := byTextFrom(m.SenderID)
	if !c.mayAnswerChange(ctx, b, m, p) {
		ch.audit(ctx, proposals.ActionRefuse, p, 0, "not allowed; "+detail)
		if bare && p.Source.Sender == m.SenderID {
			// The sender who asked cannot confirm, and the host wrote nothing
			// to answer: an ordinary "ok" is just a message.
			return "", false
		}
		return v.T("change.no_right"), true
	}
	// In a chat of two the other listed sender may answer; in a shared chat
	// only the sender whose message made the proposal.
	// Under Always confirm the answer must come from the paired sender who
	// asked, in a chat of two as well.
	if p.Source.Sender != "" && p.Source.Sender != m.SenderID && (!m.Direct || ch.alwaysConfirm(ctx, p)) {
		ch.audit(ctx, proposals.ActionRefuse, p, 0, "not the sender who asked; "+detail)
		return v.T("change.not_you"), true
	}
	if verb != "cancel" && p.Warning != "" {
		// A flagged text is confirmed in the web interface, where the owner
		// sees the warning next to the text. Dropping it is always allowed.
		ch.audit(ctx, proposals.ActionRefuse, p, 0, "flagged text; "+detail)
		return v.T("change.flagged"), true
	}
	if verb == "cancel" {
		if _, err := ch.Dismiss(ctx, b.Owner, p.ID); err != nil {
			return v.T("proposal.failed", "error", changeErr(err)), true
		}
		return v.T("proposal.cancelled"), true
	}
	done, err := ch.Confirm(ctx, b.Owner, p.ID)
	if err != nil {
		ch.audit(ctx, proposals.ActionRefuse, p, 0, "confirm failed; "+detail)
		return v.T("proposal.failed", "error", changeErr(err)), true
	}
	key := "change.done"
	switch done.Kind {
	case contract.ChangeMemory:
		key = "change.saved"
	case contract.ChangeKnowledge:
		return v.T("change.knowledge_done", "collection", done.Target, "summary", done.Summary, "minutes", int(cancelWindow/time.Minute)), true
	}
	return v.T(key, "summary", done.Summary, "minutes", int(cancelWindow/time.Minute)), true
}

// undoChange takes back the change that was applied last in this place, by the
// word "undo", when the sender may answer proposals here.
func (c *Connectors) undoChange(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, here []contract.ChangeProposal, v hostmsg.Voice) (string, bool) {
	ch := c.svc.changes
	now := c.o.Clock()
	var last *contract.ChangeProposal
	for i, p := range here {
		if p.Status == contract.ProposalApplied && !p.Auto && now.Sub(p.Answered) < cancelWindow &&
			(last == nil || p.Answered.After(last.Answered)) {
			last = &here[i]
		}
	}
	if last == nil {
		return "", false
	}
	p := *last
	if !c.mayAnswerChange(ctx, b, m, p) {
		ch.audit(ctx, proposals.ActionRefuse, p, 0, "undo not allowed; by text from sender "+m.SenderID)
		return v.T("change.no_right"), true
	}
	if p.Source.Sender != "" && p.Source.Sender != m.SenderID && (!m.Direct || ch.alwaysConfirm(ctx, p)) {
		return v.T("change.not_you"), true
	}
	if _, err := ch.Revert(ctx, b.Owner, p.ID); err != nil {
		return v.T("change.cannot", "error", changeErr(err)), true
	}
	return v.T("change.removed", "summary", p.Summary), true
}

// answerChanges handles a short answer when change proposals wait in this
// place or one was just applied. handled false leaves the message to the
// trigger path and then to the agent.
func (c *Connectors) answerChanges(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, verb, code string, isUndo bool, v hostmsg.Voice) (string, bool) {
	here := c.hereChanges(ctx, b, m)
	if len(here) == 0 {
		return "", false
	}
	now := c.o.Clock()
	var waiting, lapsed []contract.ChangeProposal
	for _, p := range here {
		switch {
		case p.Status == contract.ProposalPending && now.Before(p.Expires):
			waiting = append(waiting, p)
		case (p.Status == contract.ProposalExpired || p.Status == contract.ProposalPending) && now.Sub(p.Expires) < lapsedWindow:
			lapsed = append(lapsed, p)
		}
	}
	// The newest waits first in the list.
	slices.SortFunc(waiting, func(a, z contract.ChangeProposal) int { return z.Created.Compare(a.Created) })
	if code != "" {
		for _, p := range append(slices.Clone(waiting), lapsed...) {
			if ChangeCode(p.ID) != code {
				continue
			}
			if !slices.ContainsFunc(waiting, func(w contract.ChangeProposal) bool { return w.ID == p.ID }) {
				if !c.mayAnswerChange(ctx, b, m, p) {
					return "", false
				}
				return v.T("proposal.gone"), true
			}
			return c.answerChange(ctx, b, m, p, verb, false, v)
		}
		return "", false
	}
	if len(waiting) == 0 {
		if isUndo {
			return c.undoChange(ctx, b, m, here, v)
		}
		if len(lapsed) > 0 && c.mayAnswerChange(ctx, b, m, lapsed[0]) {
			return v.T("proposal.gone"), true
		}
		return "", false
	}
	// Several things can wait here, triggers too: a bare word answers only when
	// exactly one does.
	others := c.svc.triggers.waitingHere(ctx, PhraseFrom{Binding: b.ID, Owner: b.Owner, Chat: m.Chat, Thread: m.Thread})
	if len(waiting) == 1 && len(others) == 0 {
		return c.answerChange(ctx, b, m, waiting[0], verb, true, v)
	}
	if !c.mayAnswerChange(ctx, b, m, waiting[0]) {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString(v.T("proposal.multi"))
	for _, p := range waiting {
		fmt.Fprintf(&sb, "\n%s: %q", ChangeCode(p.ID), p.Summary)
	}
	for _, t := range others {
		fmt.Fprintf(&sb, "\n%s: %q", t.Route.Code, t.Name)
	}
	return sb.String(), true
}

// waitingHere lists the trigger proposals that wait in the place of the sender.
func (h *Triggers) waitingHere(ctx context.Context, from PhraseFrom) []triggers.Trigger {
	if h == nil {
		return nil
	}
	all, err := h.o.Store.ListTriggers(ctx, principal(from.Owner))
	if err != nil {
		return nil
	}
	now := h.o.Clock()
	var out []triggers.Trigger
	for _, t := range all {
		if t.Pending() && !t.Expired(now) && t.Route.Binding == from.Binding && sameThread(t, from) {
			out = append(out, t)
		}
	}
	return out
}
