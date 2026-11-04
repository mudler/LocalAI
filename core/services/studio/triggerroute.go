package studio

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// What a trigger that a connector chat proposed is bound by.
const (
	// ProposalTTL is how long such a proposal waits for an answer.
	ProposalTTL = 24 * time.Hour
	// MaxProposalsPerHour is how many proposals one binding may make in an
	// hour, whoever writes to it.
	MaxProposalsPerHour = 3
	// MaxReportRunes bounds the text of a run that is written to a chat.
	MaxReportRunes = 4000
	// SilentMark is the whole final message of a run that has nothing to
	// say: it is logged and delivers nothing.
	SilentMark = "[SILENT]"

	// failureAlertAfter is how many failed runs in a row raise an alert, and
	// failureAlertEvery how often one trigger may raise it.
	failureAlertAfter = 3
	failureAlertEvery = 6 * time.Hour

	reasonCapOff = "paused: capability off"
	codeLen      = 4
	// codeAlphabet leaves out the letters and digits that look alike.
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// auditKindTrigger is the Kind of the audit lines about triggers.
const auditKindTrigger contract.ChangeKind = "trigger"

// Errors of the proposals that connector chats make.
var (
	// ErrProposalExpired means nobody answered the proposal in time.
	ErrProposalExpired = errors.New("studio: this proposal expired")
	// ErrProposalRate means a binding made the most proposals of the hour.
	ErrProposalRate = fmt.Errorf("studio: at most %d trigger proposals an hour can come from one chat connection; try again later", MaxProposalsPerHour)
)

// proposalsInHour counts the proposals of a binding made in the hour before
// now, answered or not: the limit is about asking, not about waiting.
func proposalsInHour(all []triggers.Trigger, binding string, now time.Time) int {
	n := 0
	for _, t := range all {
		if t.Proposed && !t.IsChange() && t.Route.Binding == binding && now.Sub(t.Route.ProposedAt) < time.Hour {
			n++
		}
	}
	return n
}

// freshCode returns a code that no waiting proposal of the binding has.
func freshCode(all []triggers.Trigger, binding string) string {
	used := map[string]bool{}
	for _, t := range all {
		if t.Route.Binding == binding && t.Route.Code != "" {
			used[t.Route.Code] = true
		}
	}
	for {
		var b [codeLen]byte
		_, _ = rand.Read(b[:])
		for i := range b {
			b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
		}
		if c := string(b[:]); !used[c] {
			return c
		}
	}
}

// audit writes one line about a trigger that a connector chat is part of. The
// line has a hash of the prompt, never text.
func (h *Triggers) audit(ctx context.Context, action string, t triggers.Trigger, detail string) {
	ch := h.svc.changes
	if ch == nil {
		return
	}
	ev := proposals.Event{At: h.o.Clock().UTC(), Owner: t.Owner, Action: action, Proposal: t.ID, Kind: auditKindTrigger,
		Target: t.Name, Agent: t.AgentID, Hash: hashOf(t.Prompt), Detail: detail,
		Source: contract.ChangeSource{Kind: "connector", ID: t.Route.Binding, Chat: t.Route.Chat, Thread: t.Route.Thread}}
	if err := ch.store.AddEvent(ctx, ev); err != nil {
		h.log.Warn("studio: could not write the trigger audit", "action", action, "trigger", t.ID, "error", err)
	}
}

// lapse marks a waiting proposal that is past its time as expired, and returns
// the record as it is after. Another frontend may have done it first; either
// way the record that is returned is the stored one.
func (h *Triggers) lapse(ctx context.Context, t triggers.Trigger) triggers.Trigger {
	now := h.o.Clock().UTC()
	if !t.Expired(now) {
		return t
	}
	ok, err := h.o.Store.SetOutcome(ctx, t.ID, "", triggers.OutcomeExpired, now)
	if err != nil {
		h.log.Warn("studio: could not expire a trigger proposal", "trigger", t.ID, "error", err)
		return t
	}
	if cur, gerr := h.o.Store.GetTrigger(ctx, t.ID); gerr == nil {
		t = cur
	}
	if ok {
		h.audit(ctx, proposals.ActionExpire, t, "")
	}
	return t
}

// expireProposals marks every waiting proposal past its time as expired. The
// leader calls it in a pass, at most once a minute.
func (h *Triggers) expireProposals(ctx context.Context) {
	h.mu.Lock()
	if now := h.o.Clock(); now.Sub(h.swept) < time.Minute {
		h.mu.Unlock()
		return
	} else {
		h.swept = now
	}
	h.mu.Unlock()
	all, err := h.o.Store.AllTriggers(ctx)
	if err != nil {
		h.log.Warn("studio: could not list triggers to expire proposals", "error", err)
		return
	}
	now := h.o.Clock()
	for _, t := range all {
		if t.Expired(now) {
			h.lapse(ctx, t)
		}
	}
}

// ---- Confirmation by text ----

// Sentences the bot says about a proposal.
const (
	sentProposalGone    = "That proposal expired. Ask again if you still want it."
	sentProposalPlace   = "Answer in the same place where it was proposed, or open Studio."
	sentProposalNoRight = "Only a sender the owner trusts can answer a proposal from here. Open Studio to answer it."
	sentProposalFailed  = "I could not do that: %s"
	sentProposalCancel  = "Cancelled."
)

// lapsedWindow is how long after a proposal lapsed a bare word in its thread is
// answered with the news that it lapsed, and not sent to the agent.
const lapsedWindow = 15 * time.Minute

var phraseRE = regexp.MustCompile(`(?i)^\s*(confirm|cancel)\s+([a-z0-9]{4})\s*$`)

// ParsePhrase reads "confirm 7K2Q" or "cancel 7K2Q" from the whole text of a
// message. The code comes back in upper case.
func ParsePhrase(text string) (verb, code string, ok bool) {
	m := phraseRE.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), strings.ToUpper(m[2]), true
}

// PhraseFrom is who wrote a confirm or cancel phrase, and where.
type PhraseFrom struct {
	Binding, Owner, Agent string
	Chat, Thread, Sender  string
	// Roles are the roles of the sender that the kind declares.
	Roles []string
}

// AnswerByPhrase answers a waiting proposal from a short answer: a word with
// the code of the proposal, or, with an empty code, a bare word that answers
// the one proposal waiting in the place where the word is written. It also
// cancels a trigger that was made without asking, by its code, or by a bare
// word soon after. The sender must hold the capability to confirm, and write
// where the proposal was made. It returns the sentence to write back, and
// handled false when the message is an ordinary one: the text then goes to the
// agent. A proposal is answered once; every attempt is audited.
func (h *Triggers) AnswerByPhrase(ctx context.Context, from PhraseFrom, verb, code string) (reply string, handled bool) {
	all, err := h.o.Store.ListTriggers(ctx, principal(from.Owner))
	if err != nil {
		return "", false
	}
	if code == "" {
		return h.answerBare(ctx, from, verb, all)
	}
	detail := "by text from sender " + from.Sender
	for _, t := range all {
		if t.Route.Binding != from.Binding || t.Route.Code != code {
			continue
		}
		switch {
		case t.Pending():
			return h.answerProposal(ctx, from, t, verb, detail)
		case verb == "cancel" && t.Route.Auto && !t.Proposed && t.Enabled:
			return h.cancelAuto(ctx, from, t, detail)
		}
	}
	return "", false
}

// sameThread says whether the proposal or trigger was made where the sender
// writes now.
func sameThread(t triggers.Trigger, from PhraseFrom) bool {
	return t.Route.Chat == from.Chat && t.Route.Thread == from.Thread
}

// canAnswer asks the capability gate whether the sender may answer proposals
// of the binding.
func (h *Triggers) canAnswer(ctx context.Context, from PhraseFrom) bool {
	return h.svc.allowCap(ctx, CapabilityRequest{Capability: CapProposalsConfirm, Binding: from.Binding, Owner: from.Owner, Agent: from.Agent,
		Sender: from.Sender, Roles: from.Roles, Kind: "trigger"})
}

// answerBare answers with a word that has no code. It acts only when the
// meaning is clear: the sender may answer, and exactly one proposal waits in
// this place (or, for a cancel, exactly one trigger was just made here without
// asking). Otherwise the message goes on to the agent, except when several
// proposals wait: then the sender is told to use a code.
func (h *Triggers) answerBare(ctx context.Context, from PhraseFrom, verb string, all []triggers.Trigger) (string, bool) {
	now := h.o.Clock()
	var waiting, fresh, lapsed []triggers.Trigger
	for _, t := range all {
		switch {
		case t.Pending() && t.Route.Binding == from.Binding && sameThread(t, from):
			if t.Expired(now) {
				if cur := h.lapse(ctx, t); cur.Outcome == triggers.OutcomeExpired {
					lapsed = append(lapsed, cur)
				}
				continue
			}
			waiting = append(waiting, t)
		case t.Proposed && t.Outcome == triggers.OutcomeExpired && !t.IsChange() && t.Route.Binding == from.Binding && sameThread(t, from) &&
			now.Sub(t.OutcomeAt) < lapsedWindow:
			lapsed = append(lapsed, t)
		case verb == "cancel" && t.Route.Auto && !t.Proposed && t.Enabled && t.Route.Binding == from.Binding && sameThread(t, from) &&
			now.Sub(t.Route.ProposedAt) < cancelWindow:
			fresh = append(fresh, t)
		}
	}
	if len(waiting)+len(fresh) == 0 && len(lapsed) == 0 {
		return "", false
	}
	detail := "by text from sender " + from.Sender
	if len(waiting)+len(fresh) == 0 {
		// The proposal this word answers has just lapsed: say so, so the person
		// does not think it started.
		if !h.canAnswer(ctx, from) {
			return "", false
		}
		return sentProposalGone, true
	}
	if !h.canAnswer(ctx, from) {
		// An ordinary "ok" from someone who cannot answer is just a message.
		h.audit(ctx, proposals.ActionRefuse, append(waiting, fresh...)[0], "not allowed; "+detail)
		return "", false
	}
	switch {
	case len(waiting) == 1 && len(fresh) == 0:
		return h.answerProposal(ctx, from, waiting[0], verb, detail)
	case len(waiting) == 0 && len(fresh) == 1:
		return h.cancelAuto(ctx, from, fresh[0], detail)
	}
	var b strings.Builder
	b.WriteString("More than one is waiting here. Say which, for example \"confirm CODE\" or \"cancel CODE\":")
	for _, t := range append(waiting, fresh...) {
		fmt.Fprintf(&b, "\n%s: %q", t.Route.Code, t.Name)
	}
	return b.String(), true
}

// answerProposal confirms or cancels the waiting proposal p.
func (h *Triggers) answerProposal(ctx context.Context, from PhraseFrom, p triggers.Trigger, verb, detail string) (string, bool) {
	if p.Expired(h.o.Clock()) {
		h.lapse(ctx, p)
		return sentProposalGone, true
	}
	if !sameThread(p, from) {
		h.audit(ctx, proposals.ActionRefuse, p, "wrong place; "+detail)
		return sentProposalPlace, true
	}
	if !h.canAnswer(ctx, from) {
		h.audit(ctx, proposals.ActionRefuse, p, "not allowed; "+detail)
		return sentProposalNoRight, true
	}
	if verb == "cancel" {
		if err := h.DismissProposal(ctx, p.Owner, p.ID); err != nil {
			return fmt.Sprintf(sentProposalFailed, toolErr(err)), true
		}
		h.audit(ctx, proposals.ActionDismiss, p, detail)
		return sentProposalCancel, true
	}
	t, err := h.ConfirmProposal(ctx, p.Owner, p.ID, "")
	if err != nil {
		h.audit(ctx, proposals.ActionRefuse, p, "confirm failed; "+detail)
		return fmt.Sprintf(sentProposalFailed, toolErr(err)), true
	}
	h.audit(ctx, proposals.ActionConfirm, t, detail)
	return fmt.Sprintf("Confirmed. %q is on: %s. Cost: %s. %s", t.Name, h.whenText(t), h.costText(t), actsText(t)), true
}

// cancelAuto removes a trigger that was made without asking.
func (h *Triggers) cancelAuto(ctx context.Context, from PhraseFrom, t triggers.Trigger, detail string) (string, bool) {
	if !sameThread(t, from) {
		h.audit(ctx, proposals.ActionRefuse, t, "wrong place; "+detail)
		return sentProposalPlace, true
	}
	if !h.canAnswer(ctx, from) {
		h.audit(ctx, proposals.ActionRefuse, t, "not allowed; "+detail)
		return sentProposalNoRight, true
	}
	if err := h.DeleteTrigger(ctx, t.Owner, t.ID); err != nil {
		return fmt.Sprintf(sentProposalFailed, toolErr(err)), true
	}
	h.audit(ctx, proposals.ActionDismiss, t, "removed after automatic setup; "+detail)
	return fmt.Sprintf("Cancelled. %q is removed.", t.Name), true
}

// proposalText is what the host writes in the chat where a proposal was made.
// It shows what the run will do and costs, so a person can answer it from
// there, and how to answer.
func (h *Triggers) proposalText(t triggers.Trigger, note string) string {
	prompt := strings.Join(strings.Fields(t.Prompt), " ")
	if utf8.RuneCountInString(prompt) > 280 {
		prompt = string([]rune(prompt)[:280]) + "…"
	}
	where := "the Inbox in Studio"
	switch t.Route.TargetOrDefault() {
	case triggers.TargetOrigin:
		where = "this chat"
	case triggers.TargetChannel:
		where = "the channel " + t.Route.Channel
	}
	var mode string
	switch t.Route.ModeOrDefault() {
	case triggers.ModeOnChange:
		mode = ", only when the result changes"
	case triggers.ModeOnProblem:
		mode = ", only when a run fails"
	}
	when := lowerFirst(h.whenText(t))
	extra := ""
	if note != "" {
		extra = "I did not start it by myself: " + note + ".\n"
	}
	return fmt.Sprintf("Proposal %s: %q, %s. It will: %s\nCost: %s. Results go to %s%s.\n%s\n%sNothing runs until you approve. Reply \"yes\" to start it or \"cancel\" to drop it, here, within 24 hours, or open Studio. "+
		"If more than one proposal waits, reply \"confirm %s\" or \"cancel %s\".",
		t.Route.Code, t.Name, when, prompt, h.costText(t), where, mode, actsText(t), extra, t.Route.Code, t.Route.Code)
}

// Announce writes the proposal, with its code, in the chat where it was made.
// It reports whether the text was sent or queued.
func (h *Triggers) Announce(ctx context.Context, t triggers.Trigger) bool {
	return h.announce(ctx, t, "")
}

// announce is Announce with a note that says why the proposal was not made
// without asking, when the approval of the binding would have allowed it.
func (h *Triggers) announce(ctx context.Context, t triggers.Trigger, note string) bool {
	c := h.svc.connectors
	if c == nil || !t.Route.FromConnector() || t.Route.Chat == "" {
		return false
	}
	_, err := c.Post(ctx, Post{Binding: t.Route.Binding, Chat: t.Route.Chat, Thread: t.Route.Thread, Text: h.proposalText(t, note)})
	if err != nil {
		h.log.Warn("studio: could not write a proposal to its chat", "trigger", t.ID, "error", err)
		return false
	}
	return true
}

// ---- Pause ----

// capOff says whether the runs of a trigger that a connector chat proposed
// are paused because its capability is off now. A trigger made in Studio never
// is. It writes the audit line when the state changes.
func (h *Triggers) capOff(ctx context.Context, t triggers.Trigger) bool {
	if !t.Route.FromConnector() {
		return false
	}
	off := !h.svc.allowCap(ctx, CapabilityRequest{Capability: CapTriggersRun, Binding: t.Route.Binding, Owner: t.Owner, Agent: t.AgentID})
	h.mu.Lock()
	was := h.paused[t.ID]
	if h.paused == nil {
		h.paused = map[string]bool{}
	}
	h.paused[t.ID] = off
	h.mu.Unlock()
	switch {
	case off && !was:
		h.audit(ctx, proposals.ActionPause, t, reasonCapOff)
	case !off && was:
		h.audit(ctx, proposals.ActionResume, t, "")
	}
	return off
}

// ---- Delivery ----

// neutralMentions breaks the forms that notify a whole channel, so a run's
// text cannot ping everyone: a zero-width space after the opening sign.
var neutralMentions = regexp.MustCompile(`(?i)(@|<!)(channel|here|everyone|all|subteam)`)

// reportText is the text of a run as it is written to a chat: at most
// MaxReportRunes, with the mentions that notify everyone broken.
func reportText(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxReportRunes {
		s = string([]rune(s)[:MaxReportRunes]) + "…"
	}
	return neutralMentions.ReplaceAllString(s, "${1}​${2}")
}

// report delivers the end of a successful run as its trigger asks, and says
// whether the Inbox notice is left out: it is when the run is silent, when its
// mode does not call for it, or when it was written to the chat. When the
// delivery fails it returns false so that the Inbox gets the notice.
func (h *Triggers) report(ctx context.Context, t triggers.Trigger, run triggers.Run, text string) bool {
	text = strings.TrimSpace(text)
	if text == SilentMark {
		h.audit(ctx, proposals.ActionDeliver, t, "silent")
		return true
	}
	switch t.Route.ModeOrDefault() {
	case triggers.ModeOnProblem:
		return true
	case triggers.ModeOnChange:
		if !h.changed(ctx, t, run, text) {
			return true
		}
	}
	if t.Route.TargetOrDefault() == triggers.TargetChannel {
		return h.deliverChannel(ctx, t, t.Name+"\n\n"+reportText(text), func() {
			h.tell(context.WithoutCancel(ctx), t, run.ChatID, notify.KindRunEnded, notify.StatusOK, t.Name+" finished", text)
		})
	}
	if t.Route.TargetOrDefault() != triggers.TargetOrigin {
		return false
	}
	body := t.Name + "\n\n" + reportText(text)
	return h.deliverOrigin(ctx, t, body, func() {
		h.tell(context.WithoutCancel(ctx), t, run.ChatID, notify.KindRunEnded, notify.StatusOK, t.Name+" finished", text)
	})
}

// deliverOrigin writes body in the chat where the trigger was proposed. It
// checks, now and not when the trigger was made, that the binding is on and
// the capability is held. fallback is what runs when a queued message cannot
// be sent later. It reports whether the message was sent or queued.
func (h *Triggers) deliverOrigin(ctx context.Context, t triggers.Trigger, body string, fallback func()) bool {
	c := h.svc.connectors
	switch {
	case c == nil:
		h.audit(ctx, proposals.ActionDeliver, t, "refused: connectors are off")
		return false
	case !h.svc.allowCap(ctx, CapabilityRequest{Capability: CapTriggersRun, Binding: t.Route.Binding, Owner: t.Owner, Agent: t.AgentID}):
		h.audit(ctx, proposals.ActionDeliver, t, "refused: capability off")
		return false
	}
	queued, err := c.Post(ctx, Post{Binding: t.Route.Binding, Chat: t.Route.Chat, Thread: t.Route.Thread, Text: body,
		OnFail: func(error) {
			h.audit(context.Background(), proposals.ActionDeliver, t, "failed; sent to the Inbox")
			fallback()
		}})
	if err != nil {
		h.audit(ctx, proposals.ActionDeliver, t, "failed; sent to the Inbox: "+err.Error())
		return false
	}
	if queued {
		h.audit(ctx, proposals.ActionDeliver, t, "origin, queued")
	} else {
		h.audit(ctx, proposals.ActionDeliver, t, "origin, sent")
	}
	return true
}

// changed says whether the answer of a run differs from the one of the run
// before it. A run with no earlier answer is a change.
func (h *Triggers) changed(ctx context.Context, t triggers.Trigger, run triggers.Run, text string) bool {
	runs, err := h.o.Store.ListRuns(ctx, t.ID, 5)
	if err != nil {
		return true
	}
	for _, r := range runs {
		if r.ID == run.ID || r.Status != triggers.StatusDone || r.ChatID == "" {
			continue
		}
		prev := strings.TrimSpace(h.svc.finalText(ctx, r.ChatID))
		return prev == "" || prev != text
	}
	return true
}

// reportFailure raises the alert for a trigger whose last runs all failed: one
// message to the chat of a trigger that reports there, at most once in
// failureAlertEvery for each trigger. The Inbox gets every failure already.
func (h *Triggers) reportFailure(ctx context.Context, t triggers.Trigger, reason string) {
	if t.Route.TargetOrDefault() != triggers.TargetOrigin {
		return
	}
	if t.OneOff() {
		// A timer has no second run to wait for: say at once that it failed.
		h.deliverOrigin(ctx, t, fmt.Sprintf("%s failed: %s", t.Name, reportText(reason)), func() {})
		return
	}
	runs, err := h.o.Store.ListRuns(ctx, t.ID, failureAlertAfter)
	if err != nil || len(runs) < failureAlertAfter {
		return
	}
	for _, r := range runs {
		if r.Status != triggers.StatusFailed {
			return
		}
	}
	now := h.o.Clock()
	h.mu.Lock()
	if h.alerted == nil {
		h.alerted = map[string]time.Time{}
	}
	if last, ok := h.alerted[t.ID]; ok && now.Sub(last) < failureAlertEvery {
		h.mu.Unlock()
		return
	}
	h.alerted[t.ID] = now
	h.mu.Unlock()
	body := fmt.Sprintf("%s failed %d times in a row: %s\nFix or pause it in Studio.", t.Name, failureAlertAfter, reportText(reason))
	h.deliverOrigin(ctx, t, body, func() {})
}
