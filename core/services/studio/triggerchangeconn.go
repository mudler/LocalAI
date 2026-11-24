package studio

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// This file is the change, the pause, the resume, the delete and the withdrawal
// of a trigger from a chat of a messaging app. It follows the approval of the
// connection, the setting that already decides how propose_trigger works there:
//
//   - ask (the default, and auto_readonly, which cannot judge a change): the
//     request is a change record that waits for the owner. The host writes it in
//     the chat with a code, and "confirm CODE" or "cancel CODE" answers it, by a
//     sender that may confirm, in the place where it was written; Studio shows
//     the same record on a card.
//   - auto (allow all): the request is applied at once and the host tells the
//     person in the chat. A change that widens what a run may do (acts) or moves
//     its answers (delivery) is the exception: it still waits, as in Studio.
//
// The capability triggers.change must be on for the connection and held by the
// sender, checked when the tool is built and again when it is called.

// Modes that the trace and the results name.
const (
	changeModeAsk  = "ask"
	changeModeAuto = "auto"
)

// changeMode is how the approval of the connection treats a change.
func (h *Triggers) changeMode(ctx context.Context, binding string) string {
	if h.svc.connectors != nil && h.svc.connectors.ApprovalOf(ctx, binding).ModeOrDefault() == connectors.ApprovalAuto {
		return changeModeAuto
	}
	return changeModeAsk
}

// ownAgentTrigger refuses a trigger that is not the agent's own: a messaging
// chat is open to more people than the owner, so it sees only what its agent
// runs.
func ownAgentTrigger(t triggers.Trigger, agentID string) error {
	if t.AgentID != agentID {
		return errors.New("no such trigger")
	}
	return nil
}

// logChange writes the line that says which mode a change from a messaging chat
// was handled in and how it ended. It holds no message text.
func (h *Triggers) logChange(tool, mode, ref, outcome string, c builtins.Chat) {
	h.log.Info("studio: trigger change from a messaging chat", "tool", tool, "mode", mode, "ref", ref, "outcome", outcome,
		"chat", c.Actor.ChatID, "binding", c.OriginID, "agent", c.AgentID)
}

// connectorUpdate handles update_trigger in a chat of a messaging app.
func (h *Triggers) connectorUpdate(ctx context.Context, a workspace.Actor, c builtins.Chat, target triggers.Trigger, p TriggerPatch) (string, error) {
	if err := c.Require(CapTriggersChange); err != nil {
		return "", err
	}
	if err := ownAgentTrigger(target, c.AgentID); err != nil {
		return "", err
	}
	route, err := h.connectorRoute(ctx, c)
	if err != nil {
		return "", err
	}
	here := &changeAsk{Binding: route.Binding, Chat: route.Chat, Thread: route.Thread}
	mode := h.changeMode(ctx, c.OriginID)
	onlySwitch := p.Name == nil && p.Prompt == nil && p.Kind == "" && p.TZ == nil && p.DailyCap == nil && p.RunLimit == nil && p.Reads == nil &&
		p.Writes == nil && p.Widget == nil && p.EstTokens == nil && p.Acts == nil && p.Delivery == nil && p.Enabled != nil
	widens := p.Acts != nil || p.Delivery != nil
	why := ""
	if mode == changeModeAuto && widens {
		mode, why = changeModeAsk, " It changes what a run may do or where the answers go, so the owner confirms it even with allow all."
	}
	if mode == changeModeAuto {
		rec, applied, err := h.proposeChange(ctx, a.Principal, a.ChatID, target, p, nil)
		if err != nil {
			return "", toolErr(err)
		}
		if !applied {
			if _, err := h.ApplyChange(ctx, a.Principal, rec.ID); err != nil {
				_ = h.DismissChange(ctx, a.Principal, rec.ID)
				return "", toolErr(err)
			}
		}
		op := triggers.ChangeUpdate
		if onlySwitch && applied {
			op = rec.Change.Op
		}
		live, _ := h.o.Store.GetTrigger(ctx, target.ID)
		wrote := h.postChangeDone(ctx, c, op, target, live, changedFields(target, live))
		h.logChange(updateTriggerName, mode, TriggerRef(target.ID), "applied", c)
		return h.autoChangeResult(op, target.Name, wrote) + h.proofOf(ctx, target.ID), nil
	}
	rec, _, err := h.proposeChange(ctx, a.Principal, a.ChatID, target, p, here)
	if err != nil {
		return "", toolErr(err)
	}
	h.changeCard(ctx, a, rec)
	wrote := h.postChangeAsk(ctx, c, rec, target)
	h.logChange(updateTriggerName, mode, TriggerRef(target.ID), "asked", c)
	return h.askedChangeResult(rec, target.Name, wrote) + why + chatPathWarning(rec.Prompt, rec.Reads, rec.Writes) + h.proofOf(ctx, rec.ID), nil
}

// connectorDelete handles delete_trigger in a chat of a messaging app: a delete,
// or the withdrawal of a proposal that waits.
func (h *Triggers) connectorDelete(ctx context.Context, a workspace.Actor, c builtins.Chat, target triggers.Trigger) (string, error) {
	if err := c.Require(CapTriggersChange); err != nil {
		return "", err
	}
	if err := ownAgentTrigger(target, c.AgentID); err != nil {
		return "", err
	}
	route, err := h.connectorRoute(ctx, c)
	if err != nil {
		return "", err
	}
	mode := h.changeMode(ctx, c.OriginID)
	op := triggers.ChangeDelete
	if mode == changeModeAuto {
		if target.Proposed {
			if err := h.DismissProposal(ctx, a.Principal, target.ID); err != nil {
				return "", toolErr(err)
			}
			h.audit(ctx, proposals.ActionDismiss, target, "withdrawn by the agent, allow all")
			op = "withdraw"
		} else {
			rec, err := h.proposeDelete(ctx, a.Principal, a.ChatID, target, nil)
			if err != nil {
				return "", toolErr(err)
			}
			if _, err := h.ApplyChange(ctx, a.Principal, rec.ID); err != nil {
				_ = h.DismissChange(ctx, a.Principal, rec.ID)
				return "", toolErr(err)
			}
		}
		wrote := h.postChangeDone(ctx, c, op, target, target, nil)
		h.logChange(deleteTriggerName, mode, TriggerRef(target.ID), "applied", c)
		return h.autoChangeResult(op, target.Name, wrote) + h.proofOf(ctx, target.ID), nil
	}
	rec, err := h.proposeDelete(ctx, a.Principal, a.ChatID, target, &changeAsk{Binding: route.Binding, Chat: route.Chat, Thread: route.Thread})
	if err != nil {
		return "", toolErr(err)
	}
	h.changeCard(ctx, a, rec)
	wrote := h.postChangeAsk(ctx, c, rec, target)
	h.logChange(deleteTriggerName, mode, TriggerRef(target.ID), "asked", c)
	return h.askedChangeResult(rec, target.Name, wrote) + h.proofOf(ctx, rec.ID), nil
}

// opWord is the key part of an operation in the texts of the host.
func opWord(op string, target triggers.Trigger) string {
	switch {
	case op == triggers.ChangeDelete && target.Proposed:
		return "withdraw"
	case op == "withdraw":
		return "withdraw"
	}
	return op
}

func (h *Triggers) postHere(ctx context.Context, binding, chat, thread, text string) bool {
	c := h.svc.connectors
	if c == nil {
		return false
	}
	if _, err := c.Post(ctx, Post{Binding: binding, Chat: chat, Thread: thread, Text: neutralMentions.ReplaceAllString(text, "${1}​${2}")}); err != nil {
		h.log.Warn("studio: could not write a trigger change to its chat", "binding", binding, "error", err)
		return false
	}
	return true
}

// postChangeAsk writes the waiting change, with its code, in the chat it came
// from. It reports whether the chat got it.
func (h *Triggers) postChangeAsk(ctx context.Context, c builtins.Chat, rec, target triggers.Trigger) bool {
	conns := h.svc.connectors
	if conns == nil {
		return false
	}
	v := conns.voiceOf(ctx, rec.Change.Binding, "", rec.Change.Place)
	after := asTrigger(rec)
	op := opWord(rec.Change.Op, target)
	text := v.T("trigchange.ask."+op, "name", strconv.Quote(target.Name), "code", rec.Change.Code,
		"fields", strings.Join(changedFields(target, after), ", "), "when", lowerFirst(h.whenTextIn(v, after)))
	if op != "update" {
		text = v.T("trigchange.ask."+op, "name", strconv.Quote(target.Name), "code", rec.Change.Code, "when", lowerFirst(h.whenTextIn(v, target)))
	}
	h.audit(ctx, proposals.ActionPropose, changeAuditRecord(rec, target), "change:"+op)
	return h.postHere(ctx, rec.Change.Binding, rec.Change.Place, rec.Change.Thread, text)
}

// changeAuditRecord is the record that the audit line names: the trigger, in
// the place where the change was asked for.
func changeAuditRecord(rec, target triggers.Trigger) triggers.Trigger {
	target.Route.Binding, target.Route.Chat, target.Route.Thread = rec.Change.Binding, rec.Change.Place, rec.Change.Thread
	return target
}

// postChangeDone tells the chat what was done at once.
func (h *Triggers) postChangeDone(ctx context.Context, c builtins.Chat, op string, before, after triggers.Trigger, fields []string) bool {
	conns := h.svc.connectors
	if conns == nil {
		return false
	}
	chat, thread, ok := conns.PlaceOf(ctx, c.OriginID, c.Actor.ChatID)
	if !ok {
		return false
	}
	v := conns.voiceOf(ctx, c.OriginID, "", chat)
	w := opWord(op, before)
	text := v.T("trigchange.done."+w, "name", strconv.Quote(before.Name), "fields", strings.Join(fields, ", "), "when", lowerFirst(h.whenTextIn(v, after)))
	h.audit(ctx, proposals.ActionConfirm, changeAuditRecord(triggers.Trigger{Change: triggers.Change{Binding: c.OriginID, Place: chat, Thread: thread}}, before), "change:"+w+"; allow all")
	return h.postHere(ctx, c.OriginID, chat, thread, text)
}

// autoChangeResult words, for the model, a change that was applied at once. It
// follows the style of autoResult.
func (h *Triggers) autoChangeResult(op, name string, wrote bool) string {
	done := map[string]string{
		triggers.ChangeUpdate: "Changed", triggers.ChangeDelete: "Deleted", triggers.ChangePause: "Paused",
		triggers.ChangeResume: "Resumed", "withdraw": "Withdrew the proposal",
	}[op]
	if wrote {
		return fmt.Sprintf("%s %q at once, under the owner's settings. The host told the person in this chat what was done. "+
			"Say in one short sentence that it is done; do not repeat the details.", done, name)
	}
	return fmt.Sprintf("%s %q at once, under the owner's settings. The host could not write to this chat: tell the person it is done.", done, name)
}

// askedChangeResult words, for the model, a change that waits for the owner.
func (h *Triggers) askedChangeResult(rec triggers.Trigger, name string, wrote bool) string {
	what := map[string]string{
		triggers.ChangeUpdate: "a change to", triggers.ChangeDelete: "deleting", triggers.ChangePause: "pausing", triggers.ChangeResume: "resuming",
	}[rec.Change.Op]
	if rec.Change.Op == triggers.ChangeDelete {
		if t, err := h.o.Store.GetTrigger(context.Background(), rec.Change.Target); err == nil && t.Proposed {
			what = "withdrawing the proposal"
		}
	}
	if wrote {
		return fmt.Sprintf("Asked the person to confirm %s %q. Nothing is changed yet. The host wrote the request and its code in this chat; the person answers there with confirm or cancel and the code, or in Studio. "+
			"Say in one short sentence that you asked; do not repeat the code and do not say it is done.", what, name)
	}
	return fmt.Sprintf("Asked the person to confirm %s %q. Nothing is changed yet. The host could not write the code in this chat: tell the person to reply \"confirm %s\" or \"cancel %s\" here, or to open Studio. "+
		"Do not say it is done.", what, name, rec.Change.Code, rec.Change.Code)
}

// answerChange confirms or cancels the change record t that waits in a messaging
// chat. The sender must hold the capability to confirm and write where the
// request was made, as for a proposal.
func (h *Triggers) answerChange(ctx context.Context, from PhraseFrom, t triggers.Trigger, verb, detail string) (string, bool) {
	v := from.Voice
	audited := changeAuditRecord(t, asTrigger(t))
	if t.Change.Expires > 0 && h.o.Clock().Unix() >= t.Change.Expires {
		_ = h.DismissChange(ctx, t.Owner, t.ID)
		h.audit(ctx, proposals.ActionExpire, audited, "change")
		return v.T("proposal.gone"), true
	}
	if t.Change.Place != from.Chat || t.Change.Thread != from.Thread {
		h.audit(ctx, proposals.ActionRefuse, audited, "wrong place; "+detail)
		return v.T("proposal.place"), true
	}
	if !h.canAnswer(ctx, from) {
		h.audit(ctx, proposals.ActionRefuse, audited, "not allowed; "+detail)
		return v.T("proposal.no_right"), true
	}
	if verb == "cancel" {
		if err := h.DismissChange(ctx, t.Owner, t.ID); err != nil {
			return v.T("proposal.failed", "error", toolErr(err)), true
		}
		h.audit(ctx, proposals.ActionDismiss, audited, detail)
		return v.T("proposal.cancelled"), true
	}
	target, terr := h.o.Store.GetTrigger(ctx, t.Change.Target)
	if terr == nil && target.Proposed && t.Change.Op == triggers.ChangeDelete {
		if err := h.withdrawByChange(ctx, t, target); err != nil {
			h.audit(ctx, proposals.ActionRefuse, audited, "confirm failed; "+detail)
			return v.T("proposal.failed", "error", toolErr(err)), true
		}
		h.audit(ctx, proposals.ActionConfirm, audited, detail)
		return v.T("trigchange.done.withdraw", "name", strconv.Quote(target.Name)), true
	}
	applied, err := h.ApplyChange(ctx, t.Owner, t.ID)
	if err != nil {
		h.audit(ctx, proposals.ActionRefuse, audited, "confirm failed; "+detail)
		return v.T("proposal.failed", "error", toolErr(err)), true
	}
	h.audit(ctx, proposals.ActionConfirm, audited, detail)
	name := strconv.Quote(applied.Name)
	switch t.Change.Op {
	case triggers.ChangeDelete:
		return v.T("trigchange.done.delete", "name", name), true
	case triggers.ChangePause:
		return v.T("trigchange.done.pause", "name", name), true
	case triggers.ChangeResume:
		return v.T("trigchange.done.resume", "name", name, "when", lowerFirst(h.whenTextIn(v, applied))), true
	}
	return v.T("trigchange.done.update", "name", name, "fields", strings.Join(changedFields(asTrigger(t), applied), ", "), "when", lowerFirst(h.whenTextIn(v, applied))), true
}

// withdrawByChange applies a confirmed withdrawal: the proposal is declined and
// the change record is answered.
func (h *Triggers) withdrawByChange(ctx context.Context, rec, target triggers.Trigger) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	if err := h.claimChange(ctx, rec, "", triggers.OutcomeAccepted, h.o.Clock().UTC()); err != nil {
		return err
	}
	if target.Outcome == "" {
		target.Outcome, target.OutcomeAt = triggers.OutcomeDeclined, h.o.Clock().UTC()
		if err := h.o.Store.PutTrigger(ctx, target); err != nil {
			h.releaseChange(ctx, rec, triggers.OutcomeAccepted)
			return err
		}
	}
	return nil
}
