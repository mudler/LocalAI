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

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/files"
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
		// The grants of the agent are checked in Propose and Revert, for every
		// gate. Without a gate of its own the host adds nothing to them.
		g = proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })
	}
	c := &Changes{s: s, store: st, gate: g, kinds: proposals.NewKinds()}
	if err := c.registerChangeKinds(); err != nil {
		return nil, err
	}
	return c, nil
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
	// Auto marks a memory note that the host applies at once because the
	// person chose so. Only a chat of the person (Source.Kind "") can set it.
	Auto bool
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (c *Changes) audit(ctx context.Context, action string, p contract.ChangeProposal, rev int64, detail string) {
	if p.Auto && detail == "" {
		detail = "automatic: the person chose to save memory notes at once"
	}
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
		Status: contract.ProposalPending, Auto: in.Auto}
	if in.Auto && (in.Source.Kind != "" || kind.Name != contract.ChangeMemory) {
		// Defence in depth: the tool decides this too, but nothing but a memory
		// note from the person's own chat is ever applied unasked.
		c.audit(ctx, proposals.ActionRefuse, p, 0, "automatic save is for a memory note from the person's own chat")
		return contract.ChangeProposal{}, fmt.Errorf("%w: only a memory note from your own chat can be saved at once", ErrBadChange)
	}
	if err := c.s.checkChangeGrant(ctx, p.Owner, in.Agent, in.Source, kind.Grant); err != nil {
		c.audit(ctx, proposals.ActionRefuse, p, 0, err.Error())
		return contract.ChangeProposal{}, err
	}
	if err := c.gate.Allow(ctx, proposals.Request{Action: proposals.GatePropose, User: p.Owner, Agent: in.Agent, Kind: p.Kind, Scope: p.Scope, Source: in.Source}); err != nil {
		c.audit(ctx, proposals.ActionRefuse, p, 0, err.Error())
		return contract.ChangeProposal{}, err
	}
	aim := proposals.Aim{Scope: p.Scope, Agent: in.Agent, Target: in.Target, Source: in.Source}
	rel, err := kind.Path(aim)
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
	snap, err := c.snapshot(ctx, p.Owner, kind, rel, in.Content)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if err := c.check(ctx, p.Owner, kind, aim, rel, in.Content, snap); err != nil {
		return contract.ChangeProposal{}, err
	}
	p.BaseHash = snap.hash
	if snap.same {
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

// snap is the state of what a change would write, for the base hash.
type snap struct {
	hash    string // "" when no target file exists
	exists  bool
	same    bool // every file already holds the proposed text
	current string
}

// filesOf returns the files a content writes: the one target, or the files of
// a kind that writes a folder.
func filesOf(kind proposals.Kind, rel, content string) ([]proposals.FileWrite, error) {
	if kind.Files == nil {
		return []proposals.FileWrite{{Path: rel, Content: content}}, nil
	}
	fs, err := kind.Files(content)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadChange, err)
	}
	for i := range fs {
		fs[i].Path = rel + "/" + fs[i].Path
	}
	return fs, nil
}

// snapshot reads what the change would replace. For one file the hash is the
// hash of the file; for a folder it covers the path and hash of each file the
// content names, so a change to any of them makes the proposal stale.
func (c *Changes) snapshot(ctx context.Context, owner string, kind proposals.Kind, rel, content string) (snap, error) {
	fs, err := filesOf(kind, rel, content)
	if err != nil {
		return snap{}, err
	}
	var out snap
	out.same = true
	var sum strings.Builder
	for _, f := range fs {
		cur, exists, err := c.s.d.Files.ReadHome(ctx, owner, f.Path, maxTargetRead)
		if err != nil {
			return snap{}, fmt.Errorf("%w: cannot read %s: %v", ErrBadChange, f.Path, err)
		}
		out.exists = out.exists || exists
		out.same = out.same && exists && cur == f.Content
		if kind.Files == nil {
			out.current = cur
			if exists {
				out.hash = hashOf(cur)
			}
			return out, nil
		}
		h := "-"
		if exists {
			h = hashOf(cur)
		}
		sum.WriteString(f.Path + "\x00" + h + "\n")
	}
	if out.exists {
		out.hash = hashOf(sum.String())
	}
	return out, nil
}

// check runs the kind's own rules on a content.
func (c *Changes) check(ctx context.Context, owner string, kind proposals.Kind, aim proposals.Aim, rel, content string, st snap) error {
	if kind.Check == nil {
		return nil
	}
	err := kind.Check(proposals.CheckInput{Aim: aim, Path: rel, Content: content, Current: st.current, Exists: st.exists, Read: func(p string) (string, bool, error) {
		return c.s.d.Files.ReadHome(ctx, owner, p, maxTargetRead)
	}})
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBadChange, err)
	}
	return nil
}

// aimOf rebuilds where a stored proposal writes, for the checks at confirm.
func aimOf(p contract.ChangeProposal) proposals.Aim {
	return proposals.Aim{Scope: p.Scope, Agent: p.Agent, Target: targetName(p), Source: p.Source}
}

// targetName is the name the model gave, recovered from the stored path: the
// last folder for a skill or a page, which Check compares with its content.
func targetName(p contract.ChangeProposal) string {
	t := strings.TrimSuffix(p.Target, "/")
	switch p.Kind {
	case contract.ChangeSkill:
		t = strings.TrimSuffix(t, "/SKILL.md")
	case contract.ChangeMemory, contract.ChangeProfile, contract.ChangeDashboard:
		return t
	}
	if i := strings.LastIndexByte(t, '/'); i >= 0 {
		t = t[i+1:]
	}
	return t
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
	orig := p.Content
	if edited != nil {
		switch {
		case len(*edited) > kind.Limit():
			return contract.ChangeProposal{}, fmt.Errorf("%w: the text is %d bytes and a %s change may have %d", ErrBadChange, len(*edited), kind.Name, kind.Limit())
		case !utf8.ValidString(*edited) || strings.ContainsRune(*edited, 0):
			return contract.ChangeProposal{}, fmt.Errorf("%w: the text must be text", ErrBadChange)
		}
		p.Content = *edited
	}
	// The base hash covers the files the model proposed; an edit by the owner
	// may change the files to write, not what the proposal was based on.
	was, err := c.snapshot(ctx, owner, kind, p.Target, orig)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if was.hash != p.BaseHash {
		if won, _ := c.store.Settle(ctx, owner, id, contract.ProposalPending, contract.ProposalStale, c.s.now().UTC(), 0); won {
			c.audit(ctx, proposals.ActionStale, p, 0, "")
		}
		return contract.ChangeProposal{}, ErrTargetChanged
	}
	// Rules such as a lock set after the proposal apply to the text that is written.
	now, err := c.snapshot(ctx, owner, kind, p.Target, p.Content)
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	if err := c.check(ctx, owner, kind, aimOf(p), p.Target, p.Content, now); err != nil {
		return contract.ChangeProposal{}, err
	}
	writes, err := filesOf(kind, p.Target, p.Content)
	if err != nil {
		return contract.ChangeProposal{}, err
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
	rev, err := c.write(ctx, owner, writes, title, int64(kind.Limit()))
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

// write puts the files of a change into the home as one change set.
func (c *Changes) write(ctx context.Context, owner string, fs []proposals.FileWrite, title string, max int64) (int64, error) {
	if len(fs) == 1 {
		return c.s.d.Files.WriteHome(ctx, owner, fs[0].Path, fs[0].Content, title, max)
	}
	hf := make([]files.HomeFile, len(fs))
	for i, f := range fs {
		hf[i] = files.HomeFile{Path: f.Path, Content: f.Content}
	}
	return c.s.d.Files.WriteHomeFiles(ctx, owner, hf, title, max)
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
	err := c.s.checkAnyChangeGrant(ctx, user, agent, src)
	if err == nil {
		err = c.gate.Allow(ctx, proposals.Request{Action: proposals.GateRevert, User: principal(user), Agent: agent, Source: src})
	}
	if err != nil {
		c.audit(ctx, proposals.ActionRefuse, contract.ChangeProposal{Owner: principal(user), Agent: agent, Source: src}, 0, err.Error())
	}
	return err
}

// grantsAKind reports whether the grant is the one of a registered kind.
func (c *Changes) grantsAKind(grant string) bool {
	for _, n := range c.kinds.Names() {
		if k, ok := c.kinds.Lookup(string(n)); ok && k.Grant == grant {
			return true
		}
	}
	return false
}

// kindsHeld lists the kinds a chat may propose: for a chat of a saved agent
// the ones whose grant it holds.
func (c *Changes) kindsHeld(ch builtins.Chat) []contract.ChangeKind {
	var out []contract.ChangeKind
	for _, n := range c.kinds.Names() {
		if k, ok := c.kinds.Lookup(string(n)); ok && (k.Grant == "" || ch.HoldsGrant(k.Grant)) {
			out = append(out, n)
		}
	}
	return out
}
