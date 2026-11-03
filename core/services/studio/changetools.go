package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const proposeChangeDescription = "Ask to change something that lasts: a note in the user's memory, the profile of this agent, a skill, a page or the Home dashboard. " +
	"Use it when the user asks you to remember, change or forget something, gives a rule you must keep, or asks for a skill, a page or a dashboard change. " +
	"It changes nothing by itself: the owner must confirm in Studio, and the proposal expires after a day. " +
	"Until then never say that it is saved. " +
	"content is the full new text of the file, not a patch: read the current file first and keep what stays. " +
	"kind is memory (a Markdown file under Memory/), profile (the agent's standing rules, at most 4 KiB, scope agent), " +
	"skill (a full SKILL.md with name and description, target is the skill name, scope user or agent; a skill its owner locked cannot be changed), " +
	"page (a small app: target is the page name, scope user, files is an object of path to text and must hold index.html) " +
	"or dashboard (the full home.json, scope user; it must follow the schema of the localai-workspace skill). " +
	"scope is user (every chat of the person), agent (this agent only) or channel (this conversation only). " +
	"target is the file name for memory, such as index.md or food.md, the name for a skill or a page; a profile and the dashboard have no target. " +
	"summary is one line that tells the owner what changes."

// proposeChangeSchema is the schema of the tool for a chat that may propose
// the given kinds.
func proposeChangeSchema(kinds []contract.ChangeKind) string {
	enum, _ := json.Marshal(kinds)
	return `{"type":"object","properties":{` +
		`"kind":{"type":"string","enum":` + string(enum) + `},` +
		`"scope":{"type":"string","enum":["user","agent","channel"]},` +
		`"target":{"type":"string","maxLength":64,"description":"File name for a memory change, such as index.md; the name of a skill or a page."},` +
		`"content":{"type":"string","description":"The full new text of the file. For a page use files instead."},` +
		`"files":{"type":"object","additionalProperties":{"type":"string"},"description":"For a page: file paths inside the page folder and their text, such as index.html."},` +
		`"summary":{"type":"string","minLength":1,"maxLength":160,"description":"One line for the owner."}},` +
		`"required":["kind","scope","summary"]}`
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
		run                     func(ctx context.Context, c builtins.Chat, raw json.RawMessage) (string, error)
		caps                    []string
	}
	defs := []def{
		{proposeChangeName, "Propose a change", proposeChangeDescription, "", s.changes.proposeTool, changeKindCaps},
		{revertChangeName, "Revert a change", revertChangeDescription, revertChangeSchema, s.changes.revertTool, changeKindCaps},
	}
	for _, d := range defs {
		err := s.builtins.Register(builtins.Tool{
			ID: d.id, Group: builtins.GroupChanges, Label: d.label, Description: d.desc, Unlisted: true, Capabilities: d.caps,
			Build: func(_ context.Context, c builtins.Chat) (session.Tool, bool, string) {
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
				if len(kinds) == 0 {
					return session.Tool{}, false, "this agent was not given the right to change anything"
				}
				schema := d.schema
				if schema == "" {
					schema = proposeChangeSchema(kinds)
				}
				return session.Tool{Description: d.desc, Schema: json.RawMessage(schema),
					Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return d.run(ctx, c, raw) }}, true, ""
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// changeKindCaps are the capabilities that the kinds of change need in a
// connector chat. The tool is offered when the sender holds one of them; the
// call checks the one its kind needs.
var changeKindCaps = []string{contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapSkillsWrite, contract.CapPagesWrite, contract.CapDashboardWrite}

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

func (h *Changes) proposeTool(ctx context.Context, c builtins.Chat, raw json.RawMessage) (string, error) {
	var args struct {
		Kind, Scope, Target, Content, Summary string
		Files                                 map[string]string
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if err := c.Require(changeCapability(args.Kind)); err != nil {
		return "", err
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
	p, err := h.Propose(ctx, ProposeInput{User: c.Actor.Principal, Agent: c.AgentID, Source: sourceOf(c),
		Kind: args.Kind, Scope: args.Scope, Target: args.Target, Content: args.Content, Summary: args.Summary})
	if err != nil {
		return "", changeErr(err)
	}
	// The card lets the interface show Confirm and Dismiss on this tool line,
	// also after a reload.
	session.SetToolCard(ctx, session.ToolCard{Kind: ChangeProposalCardKind, ID: p.ID})
	return fmt.Sprintf("Proposal %s stored: %s (%s, %s). Nothing is saved yet: the owner must confirm it in Studio, and it expires in 24 hours. Do not say it is saved.",
		p.ID, p.Summary, p.Kind, p.Scope), nil
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
		for _, need := range changeKindCaps {
			if err := c.Require(need); err != nil {
				return "", fmt.Errorf("%w (to undo the last change without its id, every kind of change must be allowed)", err)
			}
		}
		p, err = h.RevertLast(ctx, c.Actor.Principal, c.AgentID)
	}
	if err != nil {
		return "", changeErr(err)
	}
	return fmt.Sprintf("Reverted %s (%s): the file is back to what it was before.", p.ID, p.Summary), nil
}
