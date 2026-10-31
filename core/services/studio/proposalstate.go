package studio

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// ProposalState is what became of a trigger the chat proposed.
type ProposalState string

// The states of a proposal. A removed one has no state: the trigger is gone
// from the Triggers page and the lookup answers triggers.ErrNotFound.
const (
	ProposalPending  ProposalState = "pending"
	ProposalAccepted ProposalState = "accepted"
	ProposalDeclined ProposalState = "declined"
	ProposalUndone   ProposalState = "undone"
)

// Why an accepted trigger cannot be undone. The codes are for the interface,
// which words them in the person's language; undoText is the English the
// refusal of an undo carries.
const (
	UndoRan    = "ran"
	UndoEdited = "edited"
)

var undoText = map[string]string{
	UndoRan:    "It already ran once; remove it from the Triggers page.",
	UndoEdited: "You edited it since; change or remove it on the Triggers page.",
}

// ProposalView is a proposal as the chat card shows it: its state, the record
// (the proposal itself, or the trigger that accepting made), when it was
// answered, and whether accepting can still be undone.
type ProposalView struct {
	State   ProposalState
	Trigger triggers.Trigger
	// At is when the proposal was answered; zero while it waits.
	At time.Time
	// CanUndo is set on an accepted trigger that nothing has used or changed.
	// UndoBlocked says why not otherwise: UndoRan or UndoEdited.
	CanUndo     bool
	UndoBlocked string
}

func stateOf(t triggers.Trigger) ProposalState {
	switch {
	case t.Pending():
		return ProposalPending
	case t.Outcome == triggers.OutcomeDeclined:
		return ProposalDeclined
	case t.Outcome == triggers.OutcomeUndone:
		return ProposalUndone
	}
	return ProposalAccepted
}

// ownedAny returns a record of the user, whether it is a proposal, an answered
// one or a trigger, but not a change record.
func (h *Triggers) ownedAny(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, err := h.o.Store.GetTrigger(ctx, id)
	if err != nil || t.Owner != principal(user) || t.IsChange() {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return t, nil
}

// undoBlock says why the trigger that accepting made cannot be taken back, or
// "" when it can: it ran (a run of any kind counts) or the person changed it.
func (h *Triggers) undoBlock(ctx context.Context, t triggers.Trigger) (string, error) {
	if t.Touched {
		return UndoEdited, nil
	}
	runs, err := h.o.Store.ListRuns(ctx, t.ID, 1)
	if err != nil {
		return "", err
	}
	if len(runs) > 0 {
		return UndoRan, nil
	}
	return "", nil
}

// ProposalStatus reads the state of a proposal of the user. A proposal that
// became a trigger the user removed since, or one that is not theirs, is
// triggers.ErrNotFound.
func (h *Triggers) ProposalStatus(ctx context.Context, user, id string) (ProposalView, error) {
	t, err := h.ownedAny(ctx, user, id)
	if err != nil {
		return ProposalView{}, err
	}
	if !t.Proposed && t.Outcome != triggers.OutcomeAccepted {
		return ProposalView{}, triggers.ErrNotFound // a trigger nobody proposed
	}
	v := ProposalView{State: stateOf(t), Trigger: t, At: t.OutcomeAt}
	if v.State == ProposalAccepted {
		block, err := h.undoBlock(ctx, t)
		if err != nil {
			return ProposalView{}, err
		}
		v.CanUndo, v.UndoBlocked = block == "", block
	}
	return v, nil
}

// UndoProposal takes back an accepted proposal: the trigger is switched off and
// leaves the Triggers page, and the proposal stays as a record of the answer so
// it can be scheduled again. It is refused with ErrCannotUndo once the trigger
// ran or the person changed it; the person then removes it on the Triggers page.
func (h *Triggers) UndoProposal(ctx context.Context, user, id string) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	t, err := h.ownedAny(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	if t.Proposed {
		return triggers.Trigger{}, ErrProposalAnswered
	}
	if t.Outcome != triggers.OutcomeAccepted {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	block, err := h.undoBlock(ctx, t)
	if err != nil {
		return triggers.Trigger{}, err
	}
	if block != "" {
		return triggers.Trigger{}, fmt.Errorf("%w: %s", ErrCannotUndo, undoText[block])
	}
	undone := t
	undone.Enabled, undone.Proposed, undone.Touched = false, true, false
	undone.Outcome, undone.OutcomeAt = triggers.OutcomeUndone, h.o.Clock().UTC()
	if err := h.o.Store.PutTrigger(ctx, undone); err != nil {
		return triggers.Trigger{}, err
	}
	// A pass may have started a run between the check and the write. Then the
	// trigger is the person's now: put it back.
	if block, err := h.undoBlock(ctx, t); err != nil || block != "" {
		if perr := h.o.Store.PutTrigger(ctx, t); perr != nil {
			return triggers.Trigger{}, perr
		}
		if err != nil {
			return triggers.Trigger{}, err
		}
		return triggers.Trigger{}, fmt.Errorf("%w: %s", ErrCannotUndo, undoText[block])
	}
	h.Notify()
	return undone, nil
}

// ReopenProposal makes a declined or undone proposal wait for an answer again.
func (h *Triggers) ReopenProposal(ctx context.Context, user, id string) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	t, err := h.ownedAny(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	if !t.Proposed {
		return triggers.Trigger{}, ErrProposalAnswered
	}
	if t.Pending() {
		return t, nil
	}
	mine, err := h.o.Store.ListTriggers(ctx, t.Owner)
	if err != nil {
		return triggers.Trigger{}, err
	}
	pending := 0
	for _, m := range mine {
		if m.Pending() {
			pending++
		}
	}
	if pending >= maxProposals {
		return triggers.Trigger{}, ErrTooManyProposals
	}
	t.Outcome, t.OutcomeAt, t.Touched = "", time.Time{}, false
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	return t, nil
}

// maxProposalNotes is how many answered proposals of a chat the model hears of.
const maxProposalNotes = 3

// ProposalNote is one line for the model at the start of a turn: what the user
// did with the triggers it proposed in this chat, newest first. It is empty
// when there is nothing to say. The model gets the state, not the event, so a
// later turn still knows.
func (h *Triggers) ProposalNote(ctx context.Context, user, chatID string) string {
	all, err := h.o.Store.ListTriggers(ctx, principal(user))
	if err != nil {
		return ""
	}
	var mine []triggers.Trigger
	for _, t := range all {
		if t.Outcome == "" {
			continue
		}
		if (t.IsChange() && t.Change.Chat == chatID) || (!t.IsChange() && t.SourceChatID == chatID) {
			mine = append(mine, t)
		}
	}
	if len(mine) == 0 {
		return ""
	}
	slices.SortFunc(mine, func(a, b triggers.Trigger) int { return b.OutcomeAt.Compare(a.OutcomeAt) })
	if len(mine) > maxProposalNotes {
		mine = mine[:maxProposalNotes]
	}
	parts := make([]string, 0, len(mine))
	again := false
	for _, t := range mine {
		name := t.Name
		if len(name) > 60 {
			name = name[:60]
		}
		if t.IsChange() {
			if line := changeNote(t, name); line != "" {
				parts = append(parts, line)
			}
			continue
		}
		switch t.Outcome {
		case triggers.OutcomeAccepted:
			parts = append(parts, fmt.Sprintf("%q accepted by the user and active (%s)", name, h.summary(t)))
		case triggers.OutcomeDeclined:
			parts = append(parts, fmt.Sprintf("%q declined by the user", name))
			again = true
		case triggers.OutcomeUndone:
			parts = append(parts, fmt.Sprintf("%q undone by the user after they scheduled it, so it is not active", name))
			again = true
		}
	}
	line := "Trigger proposals and changes in this chat: " + strings.Join(parts, "; ") + "."
	if again {
		line += " Do not propose a declined or undone trigger again unless the user asks."
	}
	return line
}
