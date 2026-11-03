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

// AnswerByPhrase answers the waiting proposal that has the code, when the
// phrase comes from the place it was made, and the sender holds the
// capability to confirm. It returns the sentence to write back, and handled
// false when no proposal of the binding has that code: the message is then an
// ordinary one. A proposal is answered once; every attempt is audited.
func (h *Triggers) AnswerByPhrase(ctx context.Context, from PhraseFrom, verb, code string) (reply string, handled bool) {
	all, err := h.o.Store.ListTriggers(ctx, principal(from.Owner))
	if err != nil {
		return "", false
	}
	var p triggers.Trigger
	found := false
	for _, t := range all {
		if t.Pending() && t.Route.Binding == from.Binding && t.Route.Code == code {
			p, found = t, true
			break
		}
	}
	if !found {
		return "", false
	}
	detail := "by text from sender " + from.Sender
	if p.Expired(h.o.Clock()) {
		h.lapse(ctx, p)
		return sentProposalGone, true
	}
	if p.Route.Chat != from.Chat || p.Route.Thread != from.Thread {
		h.audit(ctx, proposals.ActionRefuse, p, "wrong place; "+detail)
		return sentProposalPlace, true
	}
	if !h.svc.allowCap(ctx, CapabilityRequest{Capability: CapProposalsConfirm, Binding: from.Binding, Owner: p.Owner, Agent: from.Agent,
		Sender: from.Sender, Roles: from.Roles, Kind: "trigger"}) {
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
	return fmt.Sprintf("Confirmed. %q is on: %s.", t.Name, h.summary(t)), true
}

// proposalText is what the host writes in the chat where a proposal was made.
// It shows what the run will do, so a person can answer it from there, and
// how to answer.
func (h *Triggers) proposalText(t triggers.Trigger) string {
	prompt := strings.Join(strings.Fields(t.Prompt), " ")
	if utf8.RuneCountInString(prompt) > 280 {
		prompt = string([]rune(prompt)[:280]) + "…"
	}
	where := "the Inbox in Studio"
	if t.Route.TargetOrDefault() == triggers.TargetOrigin {
		where = "this chat"
	}
	var mode string
	switch t.Route.ModeOrDefault() {
	case triggers.ModeOnChange:
		mode = ", only when the result changes"
	case triggers.ModeOnProblem:
		mode = ", only when a run fails"
	}
	return fmt.Sprintf("Proposal %s: %q, %s. It will: %s\nResults go to %s%s.\nNothing runs until it is confirmed. Reply \"confirm %s\" or \"cancel %s\" here within 24 hours, or open Studio.",
		t.Route.Code, t.Name, strings.ToLower(h.summary(t)[:1])+h.summary(t)[1:], prompt, where, mode, t.Route.Code, t.Route.Code)
}

// Announce writes the proposal, with its code, in the chat where it was made.
// It reports whether the text was sent or queued.
func (h *Triggers) Announce(ctx context.Context, t triggers.Trigger) bool {
	c := h.svc.connectors
	if c == nil || !t.Route.FromConnector() || t.Route.Chat == "" {
		return false
	}
	_, err := c.Post(ctx, Post{Binding: t.Route.Binding, Chat: t.Route.Chat, Thread: t.Route.Thread, Text: h.proposalText(t)})
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
