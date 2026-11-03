package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Change proposals: a model asks to change the person's own state, the person
// confirms, and the host writes it as the person. The host knows no chat
// platform. A connector chat, a Studio chat and an add-on page reach it the
// same way, through the propose_change tool of the chat, and the origin of the
// chat is only recorded as the source.

var (
	// ErrChangesOff means the proposals cannot work: there are no files.
	ErrChangesOff = errors.New("studio: changes cannot be stored because files are off on this server")
	// ErrBadChange means a proposal is not valid. The text says why.
	ErrBadChange = errors.New("studio: this change is not valid")
	// ErrTargetChanged means the target changed after the proposal.
	ErrTargetChanged = errors.New("studio: the file changed since this was proposed")
	// ErrChangeExpired means the proposal waited longer than a day.
	ErrChangeExpired = errors.New("studio: this proposal expired")
	// ErrChangeAnswered means the proposal is not waiting for an answer.
	ErrChangeAnswered = errors.New("studio: this proposal was already answered")
	// ErrNotApplied means only an applied change can be reverted.
	ErrNotApplied = errors.New("studio: only a change that was applied can be reverted")
	// ErrCannotRevert means later edits of the file stop the revert.
	ErrCannotRevert = errors.New("studio: the file was edited since; change it in Files")
	// ErrNothingToRevert means the scope has no applied change.
	ErrNothingToRevert = errors.New("studio: there is no applied change to revert")
)

// maxSummaryRunes bounds the one line that tells the person what a change is.
const maxSummaryRunes = 160

// maxTargetRead bounds the target file that a proposal reads for its hash.
const maxTargetRead = 1 << 20

// Changes is the host of the change proposals.
type Changes struct {
	s     *Service
	store proposals.Store
	gate  proposals.Gate
	kinds *proposals.Kinds
}

func newChanges(s *Service) (*Changes, error) {
	st := s.d.Proposals
	if st == nil {
		var err error
		if st, err = proposals.NewJSON(""); err != nil {
			return nil, err
		}
	}
	g := s.d.ProposalGate
	if g == nil {
		g = proposals.DefaultGate
	}
	return &Changes{s: s, store: st, gate: g, kinds: proposals.NewKinds()}, nil
}

// Changes returns the host of the change proposals.
func (s *Service) Changes() *Changes { return s.changes }

// Kinds is the set of kinds the host offers. A feature registers its own.
func (c *Changes) Kinds() *proposals.Kinds { return c.kinds }

// ProposeInput is a proposal as the chat tool gives it.
type ProposeInput struct {
	User, Agent string
	Source      contract.ChangeSource
	Kind, Scope string
	Target      string
	Content     string
	Summary     string
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (c *Changes) audit(ctx context.Context, action string, p contract.ChangeProposal, rev int64, detail string) {
	ev := proposals.Event{At: c.s.now().UTC(), Owner: p.Owner, Action: action, Proposal: p.ID, Kind: p.Kind, Scope: p.Scope,
		Target: p.Target, Agent: p.Agent, Source: p.Source, Rev: rev, Hash: hashOf(p.Content), Detail: detail}
	if err := c.store.AddEvent(ctx, ev); err != nil {
		c.s.log.Warn("studio: could not write the change audit", "action", action, "proposal", p.ID, "error", err)
	}
	c.s.log.Info("studio: change proposal", "action", action, "proposal", p.ID, "kind", p.Kind, "scope", p.Scope, "agent", p.Agent, "source", p.Source.Kind, "rev", rev)
}

// sweep marks the proposals that waited too long.
func (c *Changes) sweep(ctx context.Context) {
	done, err := c.store.Expire(ctx, c.s.now().UTC())
	if err != nil {
		c.s.log.Warn("studio: could not expire change proposals", "error", err)
		return
	}
	for _, p := range done {
		c.audit(ctx, proposals.ActionExpire, p, 0, "")
	}
}

func (c *Changes) ready() error {
	if !c.s.d.Files.Available() {
		return ErrChangesOff
	}
	return nil
}

func cleanSummary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxSummaryRunes {
		s = string([]rune(s)[:maxSummaryRunes])
	}
	return s
}

// Propose stores a proposal. It changes no file.
func (c *Changes) Propose(ctx context.Context, in ProposeInput) (contract.ChangeProposal, error) {
	if err := c.ready(); err != nil {
		return contract.ChangeProposal{}, err
	}
	kind, ok := c.kinds.Lookup(in.Kind)
	if !ok {
		return contract.ChangeProposal{}, fmt.Errorf("%w: kind must be one of %v", ErrBadChange, c.kinds.Names())
	}
	if !contract.ValidChangeScope(in.Scope) {
		return contract.ChangeProposal{}, fmt.Errorf("%w: scope must be user, agent or channel", ErrBadChange)
	}
	p := contract.ChangeProposal{V: contract.ChangeProposalVersion, ID: uuid.NewString(), Owner: principal(in.User), Agent: in.Agent,
		Kind: kind.Name, Scope: contract.ChangeScope(in.Scope), Summary: cleanSummary(in.Summary), Content: in.Content, Source: in.Source,
		Status: contract.ProposalPending}
	if err := c.gate.Allow(ctx, proposals.Request{Action: proposals.GatePropose, User: p.Owner, Agent: in.Agent, Kind: p.Kind, Scope: p.Scope, Source: in.Source}); err != nil {
		c.audit(ctx, proposals.ActionRefuse, p, 0, err.Error())
		return contract.ChangeProposal{}, err
	}
	rel, err := kind.Path(proposals.Aim{Scope: p.Scope, Agent: in.Agent, Target: in.Target, Source: in.Source})
	if err != nil {
		return contract.ChangeProposal{}, fmt.Errorf("%w: %s", ErrBadChange, strings.TrimPrefix(err.Error(), proposals.ErrBadTarget.Error()+": "))
	}
	p.Target = rel
	switch {
	case len(in.Content) > kind.Limit():
		return contract.ChangeProposal{}, fmt.Errorf("%w: the content is %d bytes and a %s change may have %d", ErrBadChange, len(in.Content), kind.Name, kind.Limit())
	case !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0):
		return contract.ChangeProposal{}, fmt.Errorf("%w: the content must be text", ErrBadChange)
	case p.Summary == "":
		return contract.ChangeProposal{}, fmt.Errorf("%w: give a short summary of the change", ErrBadChange)
	}
	cur, exists, err := c.s.d.Files.ReadHome(ctx, p.Owner, rel, maxTargetRead)
	if err != nil {
		return contract.ChangeProposal{}, fmt.Errorf("%w: cannot read %s: %v", ErrBadChange, rel, err)
	}
	if exists {
		p.BaseHash = hashOf(cur)
	}
	if exists && cur == in.Content {
		return contract.ChangeProposal{}, fmt.Errorf("%w: %s has this content already", ErrBadChange, rel)
	}
	c.sweep(ctx)
	now := c.s.now().UTC()
	p.Created, p.Expires = now, now.Add(contract.ChangeExpiry)
	lim := proposals.Limits{MaxPending: contract.MaxPendingChanges, PerWindow: contract.MaxChangesPerHour, Window: time.Hour}
	if err := c.store.Create(ctx, p, lim); err != nil {
		if errors.Is(err, proposals.ErrTooManyPending) || errors.Is(err, proposals.ErrRateLimited) {
			c.audit(ctx, proposals.ActionRefuse, p, 0, err.Error())
		}
		return contract.ChangeProposal{}, err
	}
	c.audit(ctx, proposals.ActionPropose, p, 0, "")
	return p, nil
}

// List returns the proposals of the user, newest first, with the expired ones
// settled first.
func (c *Changes) List(ctx context.Context, user string, statuses ...contract.ProposalStatus) ([]contract.ChangeProposal, error) {
	c.sweep(ctx)
	return c.store.List(ctx, principal(user), statuses...)
}

// Get returns one proposal of the user.
func (c *Changes) Get(ctx context.Context, user, id string) (contract.ChangeProposal, error) {
	c.sweep(ctx)
	return c.store.Get(ctx, principal(user), id)
}

// Events returns the audit of the user, newest first.
func (c *Changes) Events(ctx context.Context, user string, limit int) ([]proposals.Event, error) {
	return c.store.Events(ctx, principal(user), limit)
}

// notPending says why a proposal cannot be answered.
func notPending(p contract.ChangeProposal) error {
	switch p.Status {
	case contract.ProposalExpired:
		return ErrChangeExpired
	case contract.ProposalStale:
		return ErrTargetChanged
	}
	return ErrChangeAnswered
}

// Confirm writes a pending proposal as the owner, in one change set.
func (c *Changes) Confirm(ctx context.Context, user, id string) (contract.ChangeProposal, error) {
	return c.confirm(ctx, user, id, nil)
}

// ConfirmEdited writes the pending proposal with the owner's own text in place
// of the model's. The text has the same limits as a proposal. The stored
// proposal keeps what the model asked for; the file and the audit hash hold
// what was written.
func (c *Changes) ConfirmEdited(ctx context.Context, user, id, content string) (contract.ChangeProposal, error) {
	return c.confirm(ctx, user, id, &content)
}

func (c *Changes) confirm(ctx context.Context, user, id string, edited *string) (contract.ChangeProposal, error) {
	if err := c.ready(); err != nil {
		return contract.ChangeProposal{}, err
	}
	owner := principal(user)
	p, err := c.Get(ctx, owner, id)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if p.Status != contract.ProposalPending {
		return contract.ChangeProposal{}, notPending(p)
	}
	kind, ok := c.kinds.Lookup(string(p.Kind))
	if !ok {
		return contract.ChangeProposal{}, fmt.Errorf("%w: this server no longer offers %s changes", ErrBadChange, p.Kind)
	}
	if edited != nil {
		switch {
		case len(*edited) > kind.Limit():
			return contract.ChangeProposal{}, fmt.Errorf("%w: the text is %d bytes and a %s change may have %d", ErrBadChange, len(*edited), kind.Name, kind.Limit())
		case !utf8.ValidString(*edited) || strings.ContainsRune(*edited, 0):
			return contract.ChangeProposal{}, fmt.Errorf("%w: the text must be text", ErrBadChange)
		}
		p.Content = *edited
	}
	cur, exists, err := c.s.d.Files.ReadHome(ctx, owner, p.Target, maxTargetRead)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	have := ""
	if exists {
		have = hashOf(cur)
	}
	if have != p.BaseHash {
		if won, _ := c.store.Settle(ctx, owner, id, contract.ProposalPending, contract.ProposalStale, c.s.now().UTC(), 0); won {
			c.audit(ctx, proposals.ActionStale, p, 0, "")
		}
		return contract.ChangeProposal{}, ErrTargetChanged
	}
	// Claim the answer first, so two confirmations write once.
	won, err := c.store.Settle(ctx, owner, id, contract.ProposalPending, contract.ProposalApplied, c.s.now().UTC(), 0)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if !won {
		return contract.ChangeProposal{}, ErrChangeAnswered
	}
	title := string(p.Kind) + ": " + p.Summary
	rev, err := c.s.d.Files.WriteHome(ctx, owner, p.Target, p.Content, title, int64(kind.Limit()))
	if err != nil {
		if _, rerr := c.store.Settle(ctx, owner, id, contract.ProposalApplied, contract.ProposalPending, time.Time{}, 0); rerr != nil {
			c.s.log.Warn("studio: could not reopen a change that failed to write", "proposal", id, "error", rerr)
		}
		return contract.ChangeProposal{}, fmt.Errorf("studio: could not write the change: %w", err)
	}
	if err := c.store.SetRev(ctx, owner, id, rev); err != nil {
		c.s.log.Warn("studio: could not record the change set of a change", "proposal", id, "rev", rev, "error", err)
	}
	p.Status, p.Rev = contract.ProposalApplied, rev
	c.audit(ctx, proposals.ActionConfirm, p, rev, "")
	return c.store.Get(ctx, owner, id)
}

// Dismiss answers a pending proposal with no.
func (c *Changes) Dismiss(ctx context.Context, user, id string) (contract.ChangeProposal, error) {
	owner := principal(user)
	p, err := c.Get(ctx, owner, id)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	won, err := c.store.Settle(ctx, owner, id, contract.ProposalPending, contract.ProposalDismissed, c.s.now().UTC(), 0)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if !won {
		return contract.ChangeProposal{}, notPending(p)
	}
	c.audit(ctx, proposals.ActionDismiss, p, 0, "")
	return c.store.Get(ctx, owner, id)
}

// Revert undoes an applied change with the change set it wrote.
func (c *Changes) Revert(ctx context.Context, user, id string) (contract.ChangeProposal, error) {
	if err := c.ready(); err != nil {
		return contract.ChangeProposal{}, err
	}
	owner := principal(user)
	p, err := c.Get(ctx, owner, id)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if p.Status != contract.ProposalApplied || p.Rev == 0 {
		return contract.ChangeProposal{}, ErrNotApplied
	}
	now := c.s.now().UTC()
	won, err := c.store.Settle(ctx, owner, id, contract.ProposalApplied, contract.ProposalReverted, now, 0)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if !won {
		return contract.ChangeProposal{}, ErrNotApplied
	}
	cs, _, err := c.s.d.Files.Store().Undo(ctx, c.s.d.Files.Actor(owner), p.Rev, workspace.UndoOptions{})
	if err != nil {
		if _, rerr := c.store.Settle(ctx, owner, id, contract.ProposalReverted, contract.ProposalApplied, p.Answered, 0); rerr != nil {
			c.s.log.Warn("studio: could not reopen a change that failed to revert", "proposal", id, "error", rerr)
		}
		if errors.Is(err, workspace.ErrConflict) {
			return contract.ChangeProposal{}, ErrCannotRevert
		}
		return contract.ChangeProposal{}, fmt.Errorf("studio: could not revert the change: %w", err)
	}
	// Record the revision of the undo; a second Settle keeps the status.
	if _, err := c.store.Settle(ctx, owner, id, contract.ProposalReverted, contract.ProposalReverted, now, cs.Rev); err != nil {
		c.s.log.Warn("studio: could not record the change set of a revert", "proposal", id, "error", err)
	}
	c.audit(ctx, proposals.ActionRevert, p, cs.Rev, "")
	return c.store.Get(ctx, owner, id)
}

// RevertLast reverts the change applied last for the user in the scope of
// agent: the changes proposed in chats of that agent, or in plain chats when
// agent is empty.
func (c *Changes) RevertLast(ctx context.Context, user, agent string) (contract.ChangeProposal, error) {
	applied, err := c.List(ctx, user, contract.ProposalApplied)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	applied = slices.DeleteFunc(applied, func(p contract.ChangeProposal) bool { return p.Agent != agent })
	if len(applied) == 0 {
		return contract.ChangeProposal{}, ErrNothingToRevert
	}
	last := slices.MaxFunc(applied, func(a, b contract.ChangeProposal) int { return a.Answered.Compare(b.Answered) })
	return c.Revert(ctx, user, last.ID)
}

// Allow asks the gate about a revert of a chat.
func (c *Changes) allowRevert(ctx context.Context, user, agent string, src contract.ChangeSource) error {
	err := c.gate.Allow(ctx, proposals.Request{Action: proposals.GateRevert, User: principal(user), Agent: agent, Source: src})
	if err != nil {
		c.audit(ctx, proposals.ActionRefuse, contract.ChangeProposal{Owner: principal(user), Agent: agent, Source: src}, 0, err.Error())
	}
	return err
}
