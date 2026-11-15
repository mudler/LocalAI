package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ChangeProposalCardKind is the Kind of the tool card that points at a change
// proposal; the card's ID is the proposal's ID.
const ChangeProposalCardKind = "change_proposal"

const (
	proposeChangeName = "propose_change"
	revertChangeName  = "revert_change"
)

// scopeSentence tells the model what the scopes mean. A chat whose agent has a
// choice of notes hears a different one: the host fixes where a note goes.
const scopeSentence = "scope is user (every chat of the person), agent (this agent only) or channel (this conversation only). "

const proposeChangeDescription = "Ask to change something that lasts: a note in the user's memory, the profile of this agent, a skill, a page or the Home dashboard. " +
	"Use it when the user asks you to remember, change or forget something, gives a rule you must keep, or asks for a skill, a page or a dashboard change. " +
	"Unless the result says the note was saved, it changes nothing by itself: the person must confirm it (the result says how), and the proposal expires after a day. " +
	"Never say that something is saved until the result says so. " +
	"content is the full new text of the file, not a patch: read the current file first and keep what stays. " +
	"kind is memory (a Markdown file under Memory/), profile (the agent's standing rules, at most 4 KiB, scope agent), " +
	"skill (a full SKILL.md with name and description, target is the skill name, scope user or agent; a skill its owner locked cannot be changed), " +
	"page (a small app: target is the page name, scope user, files is an object of path to text and must hold index.html), " +
	"dashboard (the full home.json, scope user; it must follow the schema of the localai-workspace skill) " +
	"or knowledge (a document or long text to look up later, not a short fact about the person: target is the name of a collection from list_collections, scope user, content is the text, at most 64 KiB; the owner always confirms it before it is indexed). " +
	scopeSentence +
	"target is the file name for memory, such as index.md or food.md, the name for a skill or a page, the collection for knowledge; a profile and the dashboard have no target. " +
	"summary is one line that tells the owner what changes."

// proposeChangeSchema is the schema of the tool for a chat that may propose
// the given kinds.
func proposeChangeSchema(kinds []contract.ChangeKind) string {
	return proposeChangeSchemaFor(kinds, nil)
}

// kindScopes are the scopes the kinds other than memory accept.
var kindScopes = map[contract.ChangeKind][]string{
	contract.ChangeProfile:   {"agent"},
	contract.ChangeSkill:     {"user", "agent"},
	contract.ChangePage:      {"user"},
	contract.ChangeDashboard: {"user"},
	contract.ChangeKnowledge: {"user"},
}

// proposeChangeSchemaFor is proposeChangeSchema for a chat whose agent has a
// choice of notes: memory is the scopes a memory note may go to (nil: the
// model chooses, as for a chat without an agent). With a choice the scope
// list holds only what the kinds offered accept, and a note needs no scope:
// the host puts it where the choice says.
func proposeChangeSchemaFor(kinds []contract.ChangeKind, memory []string) string {
	enum, _ := json.Marshal(kinds)
	scopes, required := `["user","agent","channel"]`, `["kind","scope","summary"]`
	if memory != nil && slices.Contains(kinds, contract.ChangeMemory) {
		want := map[string]bool{}
		for _, sc := range memory {
			want[sc] = true
		}
		for _, k := range kinds {
			for _, sc := range kindScopes[k] {
				want[sc] = true
			}
		}
		var list []string
		for _, sc := range []string{"user", "agent", "channel"} {
			if want[sc] {
				list = append(list, sc)
			}
		}
		b, _ := json.Marshal(list)
		scopes, required = string(b), `["kind","summary"]`
	}
	return `{"type":"object","properties":{` +
		`"kind":{"type":"string","enum":` + string(enum) + `},` +
		`"scope":{"type":"string","enum":` + scopes + `},` +
		`"target":{"type":"string","maxLength":64,"description":"File name for a memory change, such as index.md; the name of a skill or a page; the collection for a knowledge change."},` +
		`"content":{"type":"string","description":"The full new text of the file. For a page use files instead."},` +
		`"files":{"type":"object","additionalProperties":{"type":"string"},"description":"For a page: file paths inside the page folder and their text, such as index.html."},` +
		`"summary":{"type":"string","minLength":1,"maxLength":160,"description":"One line for the owner."}},` +
		`"required":` + required + `}`
}

// proposeChangeDescriptionFor words the tool for a chat whose notes go to
// fixed scopes: the model is told where a note goes and not to choose.
func proposeChangeDescriptionFor(memory []string) string {
	if memory == nil {
		return proposeChangeDescription
	}
	if memory[0] == string(contract.ScopeChannel) {
		// A messaging chat: the note goes to this conversation unless the
		// sender is paired and the agent's choice allows more.
		fixed := "scope is user (every chat of the person), agent (this agent only) or channel (this conversation only); the other kinds say which. " +
			"A memory note is saved to this conversation's memory (scope channel) unless the result says otherwise: leave scope out for a note. "
		return strings.Replace(proposeChangeDescription, scopeSentence, fixed, 1)
	}
	where := "the user's own memory (scope user)"
	if slices.Contains(memory, string(contract.ScopeAgent)) {
		where = "this agent's own memory (scope agent)"
	}
	fixed := "scope is user (every chat of the person) or agent (this agent only); the other kinds say which. A memory note is saved to " + where + ": the host fixes that, so leave scope out for a note. "
	return strings.Replace(proposeChangeDescription, scopeSentence, fixed, 1)
}

const revertChangeDescription = "Undo a change that the owner confirmed earlier, for example when the user says to forget that. " +
	"Give the id the earlier propose_change returned; without an id the last applied change of this agent is undone. " +
	"It works at once because it only restores the earlier text. A file edited since cannot be reverted."

const revertChangeSchema = `{"type":"object","properties":{"id":{"type":"string","description":"The id of an applied proposal."}}}`

// registerChangeTools adds propose_change and revert_change as one switch
// group. A person's chat and a connector chat can have them; an unattended
// run and a sub-agent never do. Whether a chat that runs a saved agent may use
// them is the grant of the agent; in a connector chat the sender must also hold the capability.
func (s *Service) registerChangeTools() error {
	type def struct {
		id, label, desc, schema string
		run                     func(ctx context.Context, c builtins.Chat, raw json.RawMessage, turn *autoTurn) (string, error)
		caps                    []string
	}
	defs := []def{
		{proposeChangeName, "Propose a change", proposeChangeDescription, "", s.changes.proposeTool, changeKindCaps},
		{revertChangeName, "Revert a change", revertChangeDescription, revertChangeSchema,
			func(ctx context.Context, c builtins.Chat, raw json.RawMessage, _ *autoTurn) (string, error) {
				return s.changes.revertTool(ctx, c, raw)
			}, changeKindCaps},
	}
	for _, d := range defs {
		err := s.builtins.Register(builtins.Tool{
			ID: d.id, Group: builtins.GroupChanges, Label: d.label, Description: d.desc, Unlisted: true, Capabilities: d.caps,
			Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
				switch {
				case !s.d.Files.Available():
					return session.Tool{}, false, "files are off on this server"
				case c.SubAgent:
					return session.Tool{}, false, "a sub-agent cannot change the owner's state"
				case c.Origin != "" && c.Origin != session.OriginConnector:
					return session.Tool{}, false, "a run that started by itself cannot propose changes"
				}
				// The tool exists when the chat holds a grant of some kind; it
				// offers the kinds it holds and no others.
				kinds := s.changes.kindsHeld(c)
				if !s.memoryWritable(ctx, c) {
					kinds = withoutMemory(kinds)
				}
				if !s.knowledgeOffered(ctx, c) {
					kinds = withoutKind(kinds, contract.ChangeKnowledge)
				}
				if len(kinds) == 0 {
					return session.Tool{}, false, "this agent was not given the right to change anything"
				}
				schema, desc := d.schema, d.desc
				if d.id == proposeChangeName {
					// The person can turn memory notes off: the model then does
					// not see the memory kind and cannot propose one.
					if s.changes.saveModeOf(ctx, c) == SaveNever {
						kinds = withoutKind(withoutMemory(kinds), contract.ChangeKnowledge)
						if len(kinds) == 0 {
							return session.Tool{}, false, "saving to memory is turned off in the settings of the person"
						}
					}
					// An agent with a choice of notes saves to the scope the host
					// fixes; the model is not offered another.
					fixed := s.memoryChatScopes(ctx, c)
					schema, desc = proposeChangeSchemaFor(kinds, fixed), proposeChangeDescriptionFor(fixed)
					if !slices.Contains(kinds, contract.ChangeMemory) {
						schema, desc = proposeChangeSchema(kinds), proposeChangeDescription
					}
				}
				// The count of automatic writes is for this turn's tools.
				turn := &autoTurn{}
				return session.Tool{Description: desc, Schema: json.RawMessage(schema),
					Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return d.run(ctx, c, raw, turn) }}, true, ""
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// memoryScopeWords says where a scope of notes is, for a refusal the model reads.
func memoryScopeWords(scope string) string {
	switch scope {
	case string(contract.ScopeAgent):
		return "its own memory"
	case string(contract.ScopeChannel):
		return "this conversation's memory"
	}
	return "the user's memory"
}

// changeKindCaps are the capabilities that the kinds of change need in a
// connector chat. The tool is offered when the sender holds one of them; the
// call checks the one its kind needs.
var changeKindCaps = []string{contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapSkillsWrite, contract.CapPagesWrite, contract.CapDashboardWrite, contract.CapKnowledgeWrite}

// changeCapability is the capability that a change of a kind needs. A kind
// that is not listed needs one that nobody holds.
func changeCapability(kind string) string {
	switch kind {
	case "memory":
		return contract.CapMemoryWrite
	case "profile":
		return contract.CapProfileWrite
	case "page":
		return contract.CapPagesWrite
	case "dashboard":
		return contract.CapDashboardWrite
	case "knowledge":
		return contract.CapKnowledgeWrite
	}
	return contract.CapSkillsWrite
}

func sourceOf(c builtins.Chat) contract.ChangeSource {
	return contract.ChangeSource{Kind: c.Origin, ID: c.OriginID, Chat: c.Actor.ChatID}
}

// changeErr words a failure for the model, without the package prefix.
func changeErr(err error) error {
	msg := err.Error()
	for _, e := range []error{ErrBadChange, proposals.ErrNotGranted} {
		msg = strings.TrimPrefix(msg, e.Error()+": ")
	}
	return errors.New(strings.TrimPrefix(strings.TrimPrefix(msg, "studio: "), "proposals: "))
}

func (h *Changes) proposeTool(ctx context.Context, c builtins.Chat, raw json.RawMessage, turn *autoTurn) (string, error) {
	var args struct {
		Kind, Scope, Target, Content, Summary string
		Files                                 map[string]string
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if k, ok := h.kinds.Lookup(args.Kind); ok && k.Internal {
		return "", fmt.Errorf("kind must be one of %v", h.kindsHeld(c))
	}
	if err := c.Require(changeCapability(args.Kind)); err != nil {
		return "", err
	}
	if args.Kind == string(contract.ChangeKnowledge) {
		if err := h.knowledgeGate(ctx, c, &args.Scope); err != nil {
			return "", err
		}
	}
	if memoryKindOf(args.Kind) && !h.s.memoryWritable(ctx, c) {
		return "", errors.New("memory is off here, so nothing was proposed or saved. Do not try again; say so in plain words if they ask.")
	}
	if memoryKindOf(args.Kind) {
		// The host, not the model, decides where a note of an agent with a
		// choice of notes goes. Leaving the scope out takes the one the
		// choice allows; any other is refused.
		if fixed := h.s.memoryChatScopes(ctx, c); fixed != nil {
			switch {
			case len(fixed) == 0:
				return "", errors.New("memory is off here, so nothing was proposed or saved. Do not try again; say so in plain words if they ask.")
			case args.Scope == "":
				args.Scope = fixed[0]
			case c.Origin == session.OriginConnector && !slices.Contains(fixed, args.Scope) && args.Scope != string(contract.ScopeChannel):
				return "", fmt.Errorf("only a sender who is paired with this bot can add to %s, and this sender is not. Use scope channel (this conversation's memory), or leave scope out", memoryScopeWords(args.Scope))
			case !slices.Contains(fixed, args.Scope):
				return "", fmt.Errorf("this agent saves notes to %s only (scope %s). Use that scope, or leave scope out", memoryScopeWords(fixed[0]), fixed[0])
			}
		}
	}
	if args.Files != nil {
		if args.Kind != string(contract.ChangePage) || args.Content != "" {
			return "", errors.New("files is for a page only, and a page has no content")
		}
		// A page is a set of files; the proposal keeps them as one JSON text.
		b, err := json.Marshal(map[string]any{"files": args.Files})
		if err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
		args.Content = string(b)
	}
	in := ProposeInput{User: c.Actor.Principal, Agent: c.AgentID, Source: h.sourceOf(c),
		Kind: args.Kind, Scope: args.Scope, Target: args.Target, Content: args.Content, Summary: args.Summary}
	// How a memory note is saved is the person's choice (see changemode.go).
	// Every other kind always asks.
	autoNote := ""
	if memoryKindOf(args.Kind) {
		switch h.saveModeOf(ctx, c) {
		case SaveNever:
			h.audit(ctx, proposals.ActionRefuse, contract.ChangeProposal{Owner: principal(in.User), Agent: in.Agent, Kind: contract.ChangeMemory,
				Scope: contract.ChangeScope(in.Scope), Source: in.Source}, 0, "the person turned memory notes off")
			return "", errors.New("the person turned off saving to their memory, so nothing was proposed or saved. Do not try again; say so in plain words if they ask.")
		case SaveAuto:
			if c.Origin == session.OriginConnector && args.Scope != string(contract.ScopeChannel) {
				// A messaging chat saves at once only to its own conversation.
				// Notes for the agent or the person wait for a confirmation.
				autoNote = " It was not saved at once because a note for the agent or the person needs a confirmation; a note for this conversation is saved at once."
				break
			}
			ok, why := autoFits(args.Content, turn)
			if ok {
				return h.saveNow(ctx, in, turn)
			}
			autoNote = " It was not saved at once because " + why + ", so it needs a confirmation."
		}
	}
	p, err := h.Propose(ctx, in)
	if err != nil {
		return "", changeErr(err)
	}
	// The card lets the interface show Confirm and Dismiss on this tool line,
	// also after a reload.
	session.SetToolCard(ctx, session.ToolCard{Kind: ChangeProposalCardKind, ID: p.ID})
	if c.Origin == session.OriginConnector {
		// The person reads the chat of the messaging app, not Studio: the host
		// writes the question there and the person answers it there.
		return h.connectorProposed(ctx, c, p, autoNote), nil
	}
	if p.Warning != "" {
		autoNote += " The text looks like it holds a secret: the owner sees a warning next to it. Do not repeat the secret."
	}
	return fmt.Sprintf("Proposal %s stored: %s (%s, %s). Nothing is saved yet: the owner must confirm it in Studio, and it expires in 24 hours. Do not say it is saved.%s",
		p.ID, p.Summary, p.Kind, p.Scope, autoNote), nil
}

// saveNow applies a memory note at once, for a person who chose that. The note
// goes through the whole proposal path (grant, gate, target, size, text and
// base checks, the rate limits and the audit) and is then confirmed as the
// person, so it is one change set that revert_change undoes. When the
// confirmation fails the pending proposal is dismissed, so no card is left.
func (h *Changes) saveNow(ctx context.Context, in ProposeInput, turn *autoTurn) (string, error) {
	in.Auto = true
	p, err := h.Propose(ctx, in)
	if err != nil {
		turn.give()
		return "", changeErr(err)
	}
	done, err := h.Confirm(ctx, in.User, p.ID)
	if err != nil {
		turn.give()
		if _, derr := h.Dismiss(ctx, in.User, p.ID); derr != nil {
			h.s.log.Warn("studio: could not close a note that failed to save at once", "proposal", p.ID, "error", derr)
		}
		return "", changeErr(err)
	}
	// The card lets the interface show the saved note with Undo, also after a
	// reload; it reads the proposal and sees that it was applied at once.
	session.SetToolCard(ctx, session.ToolCard{Kind: ChangeProposalCardKind, ID: done.ID})
	return fmt.Sprintf("Saved to memory: %s (%s, %s, id %s). The person chose to save notes at once, so no confirmation is needed, and the chat shows them an Undo. "+
		"To undo it, call revert_change with this id.", done.Summary, done.Kind, done.Scope, done.ID), nil
}

// knowledgeGate is the check of the tool for a knowledge change, on top of the
// grant and the capability. The level of the person for memory applies: Never
// takes the kind away. Always confirm and Ask change nothing here, because a
// knowledge change is never saved at once.
func (h *Changes) knowledgeGate(ctx context.Context, c builtins.Chat, scope *string) error {
	if h.saveModeOf(ctx, c) == SaveNever {
		h.audit(ctx, proposals.ActionRefuse, contract.ChangeProposal{Owner: principal(c.Actor.Principal), Agent: c.AgentID, Kind: contract.ChangeKnowledge,
			Scope: contract.ScopeUser, Source: h.sourceOf(c)}, 0, "the person turned saving off")
		return errors.New("the person turned off saving, so nothing was proposed. Do not try again; say so in plain words if they ask.")
	}
	if !h.s.knowledgeOffered(ctx, c) {
		return errors.New("adding to Knowledge is not available here, so nothing was proposed. Do not try again; say so in plain words if they ask.")
	}
	if *scope == "" {
		*scope = string(contract.ScopeUser)
	}
	return nil
}

// knowledgeOffered says whether a chat may propose a knowledge change at all:
// the server can add to collections, the Knowledge switch of the chat is on,
// and an agent has at least one collection on its list.
func (s *Service) knowledgeOffered(ctx context.Context, c builtins.Chat) bool {
	if !s.knowledgeAvailable(ctx, c.Actor.Principal) || slices.Contains(c.GroupsOff, builtins.GroupCollections) {
		return false
	}
	if c.AgentID == "" || s.d.Agents == nil {
		return true
	}
	ag, err := s.chatAgent(ctx, principal(c.Actor.Principal), c.AgentID)
	return err == nil && len(ag.Collections) > 0
}

func (h *Changes) revertTool(ctx context.Context, c builtins.Chat, raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	if err := h.allowRevert(ctx, c.Actor.Principal, c.AgentID, sourceOf(c)); err != nil {
		return "", changeErr(err)
	}
	// Always confirm: nothing is loosened, and undoing or forgetting is a
	// change like any other. The model cannot do it by itself; the person
	// can, with the Undo of the note or by saying undo in the messaging chat.
	// A change that is not a note is not covered, so a named id of another
	// kind still reverts.
	if h.saveModeOf(ctx, c) == SaveAlways {
		kind := ""
		if args.ID != "" {
			if q, gerr := h.Get(ctx, c.Actor.Principal, args.ID); gerr == nil {
				kind = string(q.Kind)
			}
		}
		if args.ID == "" || kind == "" || memoryKindOf(kind) {
			return "", errors.New("the person chose Always confirm for memory, so you cannot undo or forget a note yourself. " +
				"Tell them they can undo it themselves (the Undo on the note, or by sending undo in this chat), or ask them to confirm a new proposal that removes it. Do not try again.")
		}
	}
	var p contract.ChangeProposal
	var err error
	if args.ID != "" {
		// An id from another agent's scope is not this chat's to revert.
		need := contract.CapMemoryWrite
		if q, gerr := h.Get(ctx, c.Actor.Principal, args.ID); gerr == nil {
			if q.Agent != c.AgentID {
				return "", errors.New("that change belongs to another agent")
			}
			need = changeCapability(string(q.Kind))
		}
		// Undoing a change needs the right to make it.
		if err := c.Require(need); err != nil {
			return "", err
		}
		p, err = h.Revert(ctx, c.Actor.Principal, args.ID)
	} else {
		// The last change can be of any kind: the sender needs the right to
		// make each.
		// Knowledge is an optional kind: a sender without it can still undo
		// the last change, which then skips the knowledge ones.
		var skip []contract.ChangeKind
		if held, _ := c.Holds(contract.CapKnowledgeWrite); !held {
			skip = append(skip, contract.ChangeKnowledge)
		}
		for _, need := range changeKindCaps {
			if need == contract.CapKnowledgeWrite && len(skip) > 0 {
				continue
			}
			if err := c.Require(need); err != nil {
				return "", fmt.Errorf("%w (to undo the last change without its id, every kind of change must be allowed)", err)
			}
		}
		p, err = h.RevertLast(ctx, c.Actor.Principal, c.AgentID, skip...)
	}
	if err != nil {
		return "", changeErr(err)
	}
	return fmt.Sprintf("Reverted %s (%s): the file is back to what it was before.", p.ID, p.Summary), nil
}
