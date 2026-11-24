package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectortext"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A write of an add-on that waits because the connection asks first can be
// confirmed in the messaging chat where it was asked for, by a sender who is
// paired with the bot and holds proposals.confirm. The add-on keeps the
// proposal, bound to its digest; the host keeps one record here that says
// where it was asked, by whom, and how to answer it, and the answer runs the
// stored proposal exactly as it is. Nothing the model says afterwards can
// change it.

// addonHoldKind is the Kind of the record. It shares the store of the change
// proposals but is never listed with them or confirmed through them.
const addonHoldKind contract.ChangeKind = "addon-write"

// What bounds the writes that wait in the chats of one binding.
const (
	// MaxWaitingHolds is how many writes may wait for an answer at once.
	MaxWaitingHolds = 5
	// MaxHoldsPerHour is how many may be asked in an hour, answered or not,
	// whoever the sender is.
	MaxHoldsPerHour = 10
)

// AddonHolds is the add-on broker as a messaging chat uses it to answer a held
// write for a person. It is optional: a broker without it cannot ask in a chat.
type AddonHolds interface {
	// HeldState says where a stored proposal of the user is.
	HeldState(ctx context.Context, user, id string) (AddonHeld, error)
	// ConfirmHeld runs the stored proposal exactly as it is, when digest is
	// the one it holds. It marks the call as taken from a paired sender in a
	// chat. A failure is a *AddonHoldFailure.
	ConfirmHeld(ctx context.Context, user, id, digest string) (AddonHeldResult, error)
	// CancelHeld withdraws the stored proposal.
	CancelHeld(ctx context.Context, user, id string) error
}

// AddonHeld is the state of a stored proposal: pending, applied, cancelled,
// expired, rejected, executing or another word the owner uses.
type AddonHeld struct {
	State  string
	Digest string
}

// AddonHeldResult is what a confirmed proposal did.
type AddonHeldResult struct {
	// Applied is false when the add-on refused it at the apply, for example
	// because the target changed since the preview. Nothing was sent then.
	Applied bool
	// Link is an https address the add-on returned for the result, or "".
	Link string
	// ID is a number or short ID the add-on returned, or "".
	ID string
	// Undoable says the add-on kept a change set for it.
	Undoable bool
}

// AddonHoldFailure is the failure of a confirmation. Reason is a code of
// ToolReasonCodes and Status the status the service behind the add-on answered,
// or 0.
type AddonHoldFailure struct {
	Reason string
	Status int
}

func (e *AddonHoldFailure) Error() string { return "studio: add-on hold failed: " + e.Reason }

// The reasons that the outcome of a held write gets once a chat answers it.
const (
	reasonWaitingInChat = "waiting_in_chat"
	reasonChatConfirmed = "chat_confirmed"
	reasonChatCancelled = "chat_cancelled"
	reasonHoldExpired   = "hold_expired"
)

// The outcomes of a held write that a chat answered, besides held and error.
const (
	OutcomeConfirmed = "confirmed"
	OutcomeCancelled = "cancelled"
	OutcomeExpired   = "expired"
)

func (s *Service) addonHolds() AddonHolds {
	h, _ := s.d.AddonTools.(AddonHolds)
	return h
}

// holdRecords lists the records of held writes of the binding, newest first,
// in every state.
func (c *Connectors) holdRecords(ctx context.Context, owner, binding string) []contract.ChangeProposal {
	ch := c.svc.changes
	if ch == nil {
		return nil
	}
	ch.sweep(ctx)
	all, err := ch.store.List(ctx, principal(owner))
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(all, func(p contract.ChangeProposal) bool {
		return p.Kind != addonHoldKind || p.Source.ID != binding
	})
}

// holdAudit writes one line about a held write. It has a hash of the digest,
// never the call.
func (c *Connectors) holdAudit(ctx context.Context, action string, p contract.ChangeProposal, detail string) {
	if ch := c.svc.changes; ch != nil {
		ch.audit(ctx, action, p, 0, detail)
	}
}

// holdTitle names what a held write does in plain words: the title of the
// tool definition, or its tool name with spaces.
func holdTitle(h addontools.HeldProposal) string {
	if h.Title != "" {
		return h.Title
	}
	_, tool, ok := addontools.ParseToolName(h.Tool)
	if !ok {
		tool = h.Tool
	}
	return strings.NewReplacer("-", " ", "_", " ").Replace(tool)
}

// holdText is what the host writes in the chat where a write was asked for:
// what it does, to what, the facts the add-on lets a chat repeat, and how to
// answer. The facts are the add-on's own, never the model's words, and are
// cut short. Mentions that notify everyone are broken.
func holdText(v hostmsg.Voice, h addontools.HeldProposal, code string) string {
	on := ""
	if h.Resource != "" {
		on = " (" + h.Resource + ")"
	}
	var b strings.Builder
	b.WriteString(v.T("hold.head", "title", holdTitle(h), "on", on))
	for _, f := range h.Facts {
		fmt.Fprintf(&b, "\n- %s: %s", f.Key, f.Value)
	}
	b.WriteString("\n" + v.T("hold.ask", "code", code))
	return neutralMentions.ReplaceAllString(b.String(), "${1}​${2}")
}

// holdSummary is the one line the record keeps, for the audit and the lists.
func holdSummary(h addontools.HeldProposal) string {
	s := holdTitle(h)
	if h.Resource != "" {
		s += " (" + h.Resource + ")"
	}
	return cleanSummary(s)
}

// freshHoldID returns the ID of a new record whose code no waiting thing of
// the binding has: another hold, a change proposal or a trigger.
func (c *Connectors) freshHoldID(ctx context.Context, b connectors.Binding) string {
	used := map[string]bool{}
	for _, p := range c.holdRecords(ctx, b.Owner, b.ID) {
		used[ChangeCode(p.ID)] = true
	}
	if ch := c.svc.changes; ch != nil {
		if all, err := ch.store.List(ctx, principal(b.Owner), contract.ProposalPending); err == nil {
			for _, p := range all {
				used[ChangeCode(p.ID)] = true
			}
		}
	}
	if t := c.svc.triggers; t != nil {
		if all, err := t.o.Store.ListTriggers(ctx, principal(b.Owner)); err == nil {
			for _, tr := range all {
				if tr.Route.Binding == b.ID {
					used[tr.Route.Code] = true
				}
			}
		}
	}
	for {
		id := uuid.NewString()
		if !used[ChangeCode(id)] {
			return id
		}
	}
}

// canAskInChat says whether the host may write a held write in the chat of the
// binding: the owner enabled proposals.confirm for it and a paired sender
// exists who could answer.
func canAskInChat(b connectors.Binding, now time.Time) bool {
	if b.CredentialsOnly() || !slices.Contains(b.Caps, CapProposalsConfirm) {
		return false
	}
	return slices.ContainsFunc(b.Paired, func(p connectors.PairedSender) bool { return b.IsPaired(p.SenderID, now) })
}

// holdInChat asks in the messaging chat of the turn for the write that the tool
// left waiting. It returns the sentence for the model and true when the host
// wrote the proposal in the chat. When it could not (the tool always asks, no
// paired sender can answer, too many wait, the chat cannot be written) nothing
// is stored and it returns false: the write waits for the owner in the web UI.
func (c *Connectors) holdInChat(ctx context.Context, rec session.ChatRecord, chatID string, h addontools.HeldProposal) (string, bool) {
	// Only a hold that the connection's ask mode caused is answered in a chat;
	// a tool that always asks waits for the owner in the web UI.
	if rec.Origin != session.OriginConnector || h.Hold != "write_mode_propose" || c.svc.addonHolds() == nil || c.svc.changes == nil {
		return "", false
	}
	b, err := c.o.Store.GetBinding(ctx, rec.OriginID)
	if err != nil || !canAskInChat(b, c.o.Clock()) {
		return "", false
	}
	chat, thread, ok := c.PlaceOf(ctx, b.ID, chatID)
	if !ok {
		return "", false
	}
	asker := ""
	if g, found := c.svc.access.get(chatID); found && len(g.Senders) == 1 {
		asker = g.Senders[0]
	}
	now := c.o.Clock().UTC()
	waiting, inHour := 0, 0
	for _, p := range c.holdRecords(ctx, b.Owner, b.ID) {
		if p.Status == contract.ProposalPending && now.Before(p.Expires) {
			waiting++
		}
		if now.Sub(p.Created) < time.Hour {
			inHour++
		}
	}
	if waiting >= MaxWaitingHolds || inHour >= MaxHoldsPerHour {
		c.holdAudit(ctx, proposals.ActionRefuse, contract.ChangeProposal{Owner: principal(b.Owner), Kind: addonHoldKind, Target: h.ID,
			Source: contract.ChangeSource{Kind: "connector", ID: b.ID, Chat: chatID, Sender: asker}}, "too many held writes; "+byAsker(asker))
		return "", false
	}
	exp := h.ExpiresAt.UTC()
	if limit := now.Add(ProposalTTL); exp.IsZero() || exp.After(limit) {
		exp = limit
	}
	p := contract.ChangeProposal{V: contract.ChangeProposalVersion, ID: c.freshHoldID(ctx, b), Owner: principal(b.Owner), Agent: b.AgentID,
		Kind: addonHoldKind, Scope: contract.ScopeChannel, Target: h.ID, Summary: holdSummary(h), Content: h.Digest,
		Source:  contract.ChangeSource{Kind: "connector", ID: b.ID, Chat: chatID, Thread: thread, Sender: asker},
		Status:  contract.ProposalPending,
		Created: now, Expires: exp}
	if err := c.svc.changes.store.Create(ctx, p, proposals.Limits{}); err != nil {
		c.log.Warn("studio: could not store a held write", "binding", b.ID, "error", err)
		return "", false
	}
	code := ChangeCode(p.ID)
	v := c.voiceOf(ctx, b.ID, asker, chat)
	text := holdText(v, h, code)
	if _, err := c.Post(ctx, Post{Binding: b.ID, Chat: chat, Thread: thread, Text: text}); err != nil {
		c.log.Warn("studio: could not write a held write to its chat", "binding", b.ID, "error", err)
		_, _ = c.svc.changes.store.Settle(ctx, p.Owner, p.ID, contract.ProposalPending, contract.ProposalDismissed, now, 0)
		return "", false
	}
	c.holdAudit(ctx, proposals.ActionPropose, p, "asked in the chat; "+byAsker(asker))
	c.noteHold(ctx, p, session.HostNoteHoldWaiting, v.T("hold.note_waiting", "title", holdTitle(h), "code", code))
	return "Proposal " + h.ID + " stored: " + holdSummary(h) + ". Nothing has been changed. The host wrote the proposal in this chat, with what it will do, " +
		"and it is waiting for confirmation in this chat, code " + code + ": a person paired with this bot answers there with yes or no, or \"confirm " + code +
		"\" or \"cancel " + code + "\", within 24 hours. Say in one short sentence that it waits for confirmation here; do not repeat the text, " +
		"and do not say it was made.", true
}

func byAsker(sender string) string {
	if sender == "" {
		return "asker unknown"
	}
	return "asked by sender " + sender
}

// noteHold shows in the chat record of the owner what happened to a held
// write. It never blocks and only logs a failure.
func (c *Connectors) noteHold(ctx context.Context, p contract.ChangeProposal, code, text string) {
	if p.Source.Chat == "" {
		return
	}
	e := session.Entry{Role: session.RoleHostNote, Note: code, Text: cutRunes(text, 400)}
	if err := c.svc.NoteEntry(context.WithoutCancel(ctx), p.Owner, p.Source.Chat, e); err != nil {
		c.log.Warn("studio: could not note a held write in its chat", "proposal", p.ID, "error", err)
	}
}

// recordHoldEvent adds the answer to a held write to the latest decisions of
// the binding.
func (c *Connectors) recordHoldEvent(p contract.ChangeProposal, outcome, reason string, status int, by string) {
	tool, addon := p.Summary, ""
	b, err := c.o.Store.GetBinding(context.Background(), p.Source.ID)
	kind := ""
	if err == nil {
		kind = b.Kind
	}
	senders := []string{}
	if by != "" {
		senders = append(senders, by)
	}
	e := AccessEvent{At: c.o.Clock().UTC(), Binding: p.Source.ID, Kind: kind, Senders: senders, Addon: addon, Tool: tool, Outcome: outcome,
		Reason: reason, Status: status, Asker: p.Source.Sender}
	c.svc.access.recordTool(e)
}

// holdPlaceMatches says whether the record was asked in the place where m is
// written now.
func (c *Connectors) holdPlaceMatches(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, p contract.ChangeProposal) bool {
	chat, thread, ok := c.PlaceOf(ctx, b.ID, p.Source.Chat)
	return ok && chat == m.Chat && thread == m.Thread
}

// mayAnswerHold says whether the sender may confirm or drop a held write: the
// sender must be paired with the bot, and the capability gate must let them.
func (c *Connectors) mayAnswerHold(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) bool {
	if m.SenderID == "" || !b.IsPaired(m.SenderID, c.o.Clock()) {
		return false
	}
	return c.svc.allowCap(ctx, CapabilityRequest{Capability: CapProposalsConfirm, Binding: b.ID, Owner: b.Owner, Agent: b.AgentID,
		Sender: m.SenderID, Username: m.SenderUsername, Roles: m.Roles, Chat: m.Chat, Direct: m.Direct, Kind: string(addonHoldKind), Sensitive: true})
}

// waitingOthers lists the codes and names of the change proposals and triggers
// that wait in the place where m is written, so that a bare word with several
// things waiting asks which.
func (c *Connectors) waitingOthers(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) [][2]string {
	var out [][2]string
	for _, p := range c.hereChanges(ctx, b, m) {
		if p.Status == contract.ProposalPending && c.o.Clock().Before(p.Expires) {
			out = append(out, [2]string{ChangeCode(p.ID), p.Summary})
		}
	}
	for _, t := range c.svc.triggers.waitingHere(ctx, PhraseFrom{Binding: b.ID, Owner: b.Owner, Chat: m.Chat, Thread: m.Thread}) {
		out = append(out, [2]string{t.Route.Code, t.Name})
	}
	return out
}

// answerHolds answers a held write from a short answer. handled false leaves
// the message to the change proposals, the triggers and then the agent.
func (c *Connectors) answerHolds(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, verb, code string, v hostmsg.Voice) (string, bool) {
	recs := c.holdRecords(ctx, b.Owner, b.ID)
	if len(recs) == 0 {
		return "", false
	}
	now := c.o.Clock()
	if code != "" {
		for _, p := range recs {
			if ChangeCode(p.ID) != code || p.Status != contract.ProposalPending && p.Status != contract.ProposalExpired &&
				now.Sub(p.Answered) > lapsedWindow {
				continue
			}
			return c.answerHold(ctx, b, m, p, verb, v)
		}
		return "", false
	}
	var here []contract.ChangeProposal
	for _, p := range recs {
		if p.Status == contract.ProposalPending && now.Before(p.Expires) && c.holdPlaceMatches(ctx, b, m, p) {
			here = append(here, p)
		}
	}
	if len(here) == 0 {
		return "", false
	}
	slices.SortFunc(here, func(a, z contract.ChangeProposal) int { return z.Created.Compare(a.Created) })
	others := c.waitingOthers(ctx, b, m)
	if len(here) == 1 && len(others) == 0 {
		return c.answerHold(ctx, b, m, here[0], verb, v)
	}
	if !c.mayAnswerHold(ctx, b, m) {
		return v.T("hold.no_right"), true
	}
	var sb strings.Builder
	sb.WriteString(v.T("proposal.multi"))
	for _, p := range here {
		fmt.Fprintf(&sb, "\n%s: %q", ChangeCode(p.ID), p.Summary)
	}
	for _, o := range others {
		fmt.Fprintf(&sb, "\n%s: %q", o[0], o[1])
	}
	return sb.String(), true
}

// lapsedHoldReply tells a sender that the write their bare word answers has
// expired, when nothing else waits here and one expired a moment ago.
func (c *Connectors) lapsedHoldReply(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, v hostmsg.Voice) (string, bool) {
	now := c.o.Clock()
	for _, p := range c.holdRecords(ctx, b.Owner, b.ID) {
		if p.Status == contract.ProposalExpired && now.Sub(p.Expires) < lapsedWindow && c.holdPlaceMatches(ctx, b, m, p) && c.mayAnswerHold(ctx, b, m) {
			return v.T("proposal.gone"), true
		}
	}
	return "", false
}

// answerHold confirms or drops the held write p for the sender of m.
func (c *Connectors) answerHold(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, p contract.ChangeProposal, verb string, v hostmsg.Voice) (string, bool) {
	detail := byTextFrom(m.SenderID) + "; " + byAsker(p.Source.Sender)
	if !c.mayAnswerHold(ctx, b, m) {
		c.holdAudit(ctx, proposals.ActionRefuse, p, "not allowed; "+detail)
		return v.T("hold.no_right"), true
	}
	if !c.holdPlaceMatches(ctx, b, m, p) {
		c.holdAudit(ctx, proposals.ActionRefuse, p, "wrong place; "+detail)
		return v.T("proposal.place"), true
	}
	now := c.o.Clock().UTC()
	if p.Status == contract.ProposalExpired || p.Status == contract.ProposalPending && !now.Before(p.Expires) {
		c.endHold(ctx, p, contract.ProposalExpired, OutcomeExpired, reasonHoldExpired, 0, "")
		return v.T("proposal.gone"), true
	}
	if p.Status != contract.ProposalPending {
		return v.T("hold.closed"), true
	}
	holds := c.svc.addonHolds()
	if holds == nil {
		return v.T("proposal.failed", "error", "add-ons cannot be reached"), true
	}
	owner := p.Owner
	state, err := holds.HeldState(ctx, owner, p.Target)
	switch {
	case err != nil:
		return v.T("proposal.failed", "error", c.holdReason(v, "unavailable", 0)), true
	case state.State != "pending":
		// The owner answered it in the web UI, or it lapsed there.
		outcome, status := OutcomeCancelled, contract.ProposalDismissed
		if state.State == "applied" {
			outcome, status = OutcomeConfirmed, contract.ProposalApplied
		}
		c.endHold(ctx, p, status, outcome, "", 0, "")
		return v.T("hold.closed"), true
	case state.Digest != p.Content:
		c.holdAudit(ctx, proposals.ActionRefuse, p, "digest differs; "+detail)
		return v.T("proposal.failed", "error", c.holdReason(v, "forbidden", 0)), true
	}
	if verb == "cancel" {
		won, err := c.svc.changes.store.Settle(ctx, owner, p.ID, contract.ProposalPending, contract.ProposalDismissed, now, 0)
		if err != nil || !won {
			return v.T("hold.closed"), true
		}
		if err := holds.CancelHeld(ctx, owner, p.Target); err != nil {
			c.log.Warn("studio: could not cancel a held write", "proposal", p.ID, "error", err)
		}
		c.holdAudit(ctx, proposals.ActionDismiss, p, detail)
		c.recordHoldEvent(p, OutcomeCancelled, reasonChatCancelled, 0, m.SenderID)
		text := v.T("hold.cancelled")
		c.noteHold(ctx, p, session.HostNoteHoldCancelled, text)
		return text, true
	}
	// One answer wins: the record is claimed before anything is sent, so a
	// second "yes" at the same moment, on this frontend or another, does nothing.
	won, err := c.svc.changes.store.Settle(ctx, owner, p.ID, contract.ProposalPending, contract.ProposalApplied, now, 0)
	if err != nil || !won {
		return v.T("hold.closed"), true
	}
	res, err := holds.ConfirmHeld(ctx, owner, p.Target, p.Content)
	if err == nil && !res.Applied {
		err = &AddonHoldFailure{Reason: "stale"}
	}
	if err != nil {
		var f *AddonHoldFailure
		if !errors.As(err, &f) {
			f = &AddonHoldFailure{Reason: "error"}
		}
		_, _ = c.svc.changes.store.Settle(ctx, owner, p.ID, contract.ProposalApplied, contract.ProposalStale, now, 0)
		c.holdAudit(ctx, proposals.ActionRefuse, p, "confirm failed ("+f.Reason+"); "+detail)
		c.recordHoldEvent(p, addontools.OutcomeError, f.Reason, f.Status, m.SenderID)
		text := v.T("proposal.failed", "error", c.holdReason(v, f.Reason, f.Status))
		c.noteHold(ctx, p, session.HostNoteHoldFailed, text)
		return text, true
	}
	c.holdAudit(ctx, proposals.ActionConfirm, p, detail)
	c.recordHoldEvent(p, OutcomeConfirmed, reasonChatConfirmed, 0, m.SenderID)
	text := v.T("hold.done", "title", p.Summary)
	if res.Link != "" {
		text += " " + res.Link
	} else if res.ID != "" {
		text += " " + res.ID
	}
	if res.Undoable {
		text += " " + v.T("hold.undoable")
	} else {
		text += " " + v.T("hold.not_undoable")
	}
	c.noteHold(ctx, p, session.HostNoteHoldConfirmed, text)
	return text, true
}

// holdReason is the plain reason of a failure, in the language of the voice.
func (c *Connectors) holdReason(v hostmsg.Voice, reason string, status int) string {
	if s, ok := connectortext.ToolReason(v.Lang, ToolReasonKey(reason), status); ok {
		return s
	}
	return ToolReasonMessage(reason, status)
}

// endHold settles a record whose proposal ended without a chat answer: it
// expired, or the owner answered it in the web UI.
func (c *Connectors) endHold(ctx context.Context, p contract.ChangeProposal, to contract.ProposalStatus, outcome, reason string, status int, by string) {
	won, err := c.svc.changes.store.Settle(ctx, p.Owner, p.ID, contract.ProposalPending, to, c.o.Clock().UTC(), 0)
	if err != nil || !won {
		return
	}
	c.recordHoldEvent(p, outcome, reason, status, by)
}

// holdLapsed is called for a record that the store expired: the chat of the
// owner and the latest decisions show it, and the log has a line.
func (c *Connectors) holdLapsed(ctx context.Context, p contract.ChangeProposal) {
	c.recordHoldEvent(p, OutcomeExpired, reasonHoldExpired, 0, "")
	v := c.voiceOf(ctx, p.Source.ID, "", "")
	c.noteHold(ctx, p, session.HostNoteHoldExpired, v.T("hold.note_expired", "title", p.Summary))
}
