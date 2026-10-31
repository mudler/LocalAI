package studio

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The built-in bash runs commands directly on this machine, with no isolation
// and with the network of the server (see hostexec). It serves when no Sandbox
// add-on does: none is installed, it is switched off for the chat, stopped, or
// cannot run. An administrator chooses with chats_host_exec who gets it: off,
// ask (administrators, and each command waits for their yes in the chat) or
// on (everyone, no card).

// HostExec runs the built-in bash. *hostexec.Runner implements it.
type HostExec interface {
	// Unavailable says why this server cannot run commands, or "".
	Unavailable() string
	// Tool is the tool definition of a chat.
	Tool(a workspace.Actor) session.Tool
}

var _ HostExec = (*hostexec.Runner)(nil)

// HostExecCardKind is the card of a call of the built-in bash. Its ID says
// what happened: ran (no card was needed), once (the person allowed it),
// denied, timeout (nobody answered), admin (only an administrator can allow
// it). The interface shows it on the tool line, so a reload still says that
// the command ran on this machine and who allowed it.
const HostExecCardKind = "host_exec"

// The answers of the consent card.
const (
	HostExecOnce = "once"
	HostExecDeny = "deny"
)

// HostExecDecision is the answer of the consent card, sent on the chat bus to
// the frontend that runs the turn.
type HostExecDecision struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// ErrHostExecDecision means the answer is not one the card offers, or the
// person who sent it may not allow commands on this machine.
var ErrHostExecDecision = errors.New("studio: only an administrator can allow commands on this machine")

// ErrHostExecExpired means the card has nobody waiting on it: the turn that
// asked ended (or its frontend died), or a newer card replaced it. The
// command did not run, and no answer can run it now.
var ErrHostExecExpired = errors.New("studio: the card expired; the command did not run")

// ErrHostExecAnswered means the card was answered already with the other
// answer. The same answer again is not an error.
var ErrHostExecAnswered = errors.New("studio: the card was answered already")

// hostExecWait is how long a command waits for the card. The card is kept with
// the chat, so a reload shows it again; the call ends with "no answer" when
// nobody answers in time.
var hostExecWait = 5 * time.Minute

func (s *Service) hostExecMode() string {
	if s.d.HostExecMode == nil {
		return hostexec.ModeOff
	}
	switch m := s.d.HostExecMode(); m {
	case hostexec.ModeAsk, hostexec.ModeOn:
		return m
	}
	return hostexec.ModeOff
}

func (s *Service) isAdmin(ctx context.Context, user string) bool {
	if s.d.IsAdmin == nil {
		return true // no accounts: the one person is the administrator
	}
	return s.d.IsAdmin(ctx, user)
}

// registerHostBash adds the built-in bash to the registry of built-in tools.
func (s *Service) registerHostBash() error {
	if s.d.HostExec == nil {
		return nil
	}
	return s.builtins.Register(builtins.Tool{
		ID:    "bash",
		Label: "Commands (bash)",
		Description: "Runs commands and code for the chats directly on this machine, with no isolation: commands can read what LocalAI can read " +
			"and reach the internet and your network. The Sandbox add-on replaces it with isolated commands.",
		Setting:     builtins.SettingHostExec,
		Replaceable: true,
		Enabled:     func() bool { return s.hostExecMode() != hostexec.ModeOff },
		Build:       s.buildHostBash,
	})
}

// buildHostBash gives a chat the built-in bash when the setting allows it.
func (s *Service) buildHostBash(_ context.Context, c builtins.Chat) (session.Tool, bool, string) {
	if r := s.d.HostExec.Unavailable(); r != "" {
		return session.Tool{}, false, r
	}
	if !s.d.Files.Available() {
		return session.Tool{}, false, "the files are off"
	}
	mode := s.hostExecMode()
	if c.Origin != "" && !c.Agent {
		// An unattended run without an agent never gets a tool that acts at
		// once, as with the add-ons' action tools.
		return session.Tool{}, false, "an unattended run without an agent"
	}
	if mode == hostexec.ModeAsk {
		switch {
		case c.SubAgent || c.Origin != "":
			return session.Tool{}, false, "each command needs a person's yes, and nobody watches this chat"
		case !c.Admin:
			return session.Tool{}, false, "only an administrator can allow commands on this machine"
		}
	}
	t := s.d.HostExec.Tool(c.Actor)
	a := c.Actor
	run := s.withCommandFiles(a, t.Run)
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		return s.runHostBash(ctx, a, run, args)
	}
	return t, true, ""
}

// runHostBash runs one call, after the card when the mode asks for one.
func (s *Service) runHostBash(ctx context.Context, a workspace.Actor, run func(context.Context, json.RawMessage) (string, error), args json.RawMessage) (string, error) {
	outcome := "ran"
	switch s.hostExecMode() {
	case hostexec.ModeOff:
		return "Commands on this machine were switched off by an administrator. Do not try again.", nil
	case hostexec.ModeAsk:
		if !s.isAdmin(ctx, a.Principal) {
			session.SetToolCard(ctx, session.ToolCard{Kind: HostExecCardKind, ID: "admin"})
			return "Not run: only an administrator can allow commands on this machine. Tell the user to ask one, or give them the command to run.", nil
		}
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(args, &in)
		switch d := s.askHostExec(ctx, a, in.Command); d {
		case HostExecOnce:
			outcome = "once"
		case "stopped":
			return "", ctx.Err()
		case "timeout":
			session.SetToolCard(ctx, session.ToolCard{Kind: HostExecCardKind, ID: "timeout"})
			return "Not run: the user did not answer the card that asks to run this command on this machine. Do not try again; ask them.", nil
		default:
			session.SetToolCard(ctx, session.ToolCard{Kind: HostExecCardKind, ID: "denied"})
			return "Not run: the user did not allow this command on this machine. Do not try again; give them the command, or ask what to do.", nil
		}
	}
	out, err := run(ctx, args)
	if err == nil {
		session.SetToolCard(ctx, session.ToolCard{Kind: HostExecCardKind, ID: outcome})
		// What the command wrote is in the user's files now.
		s.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindFiles})
	}
	return out, err
}

// askHostExec shows the card and waits for its answer: once, deny, timeout,
// or stopped when the turn ends first. The answer is stored with the card
// before it goes on the chat bus; the call wakes on the message, or finds the
// answer when it reads the card again every two seconds, as the network card
// does, so a lost message does not cost the person's click.
func (s *Service) askHostExec(ctx context.Context, a workspace.Actor, command string) string {
	id := newID()
	k := key(a.Principal, a.ChatID)
	wake, cancel := s.listenConsent(k, func(m ControlMsg) bool { return m.HostExec != nil && m.HostExec.ID == id })
	defer cancel()
	if len(command) > 2000 {
		command = strings.ToValidUTF8(command[:2000], "") + "..."
	}
	// The card is kept with the chat while it waits, so a reload, or another
	// frontend, shows it again and can answer it. It names the turn that
	// asked, so a card that a dead frontend leaves behind is not taken for a
	// waiting one.
	turn := consentTurn(s.chatTurn(ctx, a.Principal, a.ChatID))
	s.setHostConsent(ctx, a, &session.HostConsent{ID: id, Command: command, Turn: turn, AskedAt: s.now().UTC()})
	defer s.clearHostConsent(context.WithoutCancel(ctx), a.Principal, a.ChatID, id)
	s.bus.Publish(k, session.Event{Kind: session.KindHostExecConsent, IDs: []string{id}, Text: command})
	decision := ""
	if s.waitConsent(ctx, wake, hostExecWait, func() bool {
		rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
		if err != nil {
			return false
		}
		c := rec.HostConsent
		switch {
		case c == nil || c.ID != id:
			// Cleared by the next turn or the reaper: this turn is over.
			decision = "timeout"
		case c.Decision != "":
			decision = c.Decision
		default:
			return false
		}
		return true
	}) {
		return decision
	}
	if ctx.Err() != nil {
		return "stopped"
	}
	return "timeout"
}

// clearHostConsent clears the card id, and leaves a newer card alone.
func (s *Service) clearHostConsent(ctx context.Context, p, conv, id string) {
	err := s.d.Store.UpdateHostConsent(ctx, p, conv, func(cur *session.HostConsent) (*session.HostConsent, error) {
		if cur == nil || cur.ID != id {
			return cur, nil
		}
		return nil, nil
	})
	if err != nil {
		slog.Warn("Chats: could not clear the consent card of the chat", "chat", conv, "error", err)
	}
}

// liveHostConsent returns the card of rec when it still has a waiter, by the
// rule the network cards follow; nil otherwise.
func (s *Service) liveHostConsent(rec session.ChatRecord, info store.TurnInfo) *session.HostConsent {
	c := rec.HostConsent
	if c == nil {
		return nil
	}
	asked := c.AskedAt
	if asked.IsZero() {
		// A card stored before cards named their turn: trust the turn alone.
		asked = s.now()
		if !info.Running {
			return nil
		}
	}
	if !consentLive(c.Turn, asked, hostExecWait, info, s.now()) {
		return nil
	}
	return c
}

// HostConsent returns the consent card of the chat that waits for an answer,
// or nil: a card whose turn is over, or that a frontend that died left, is
// not shown. The chat of another user is session.ErrNotFound.
func (s *Service) HostConsent(ctx context.Context, user string, rec session.ChatRecord, info store.TurnInfo) *session.HostConsent {
	if rec.Principal != principal(user) {
		return nil
	}
	c := s.liveHostConsent(rec, info)
	if c == nil || c.Decision != "" {
		// An answered card is about to go: the call has its answer.
		return nil
	}
	return c
}

// setHostConsent stores or clears the card that waits. A failure only costs
// the card after a reload; the live card still works.
func (s *Service) setHostConsent(ctx context.Context, a workspace.Actor, c *session.HostConsent) {
	if err := s.d.Store.SetHostConsent(ctx, a.Principal, a.ChatID, c); err != nil {
		slog.Warn("Chats: could not keep the consent card with the chat", "chat", a.ChatID, "error", err)
	}
}

// HostExecDecide answers the consent card of a chat. Only an administrator
// may allow a command; anyone who owns the chat may refuse it. The chat of
// another user is session.ErrNotFound.
func (s *Service) HostExecDecide(ctx context.Context, user, conv, id, decision string) error {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return err
	}
	if id == "" || len(id) > 64 {
		return ErrHostExecDecision
	}
	switch decision {
	case HostExecDeny:
	case HostExecOnce:
		if !s.isAdmin(ctx, p) {
			return ErrHostExecDecision
		}
	default:
		return ErrHostExecDecision
	}
	// An answer to a card nobody waits on reaches no one: say so, rather
	// than a success the command never follows.
	if c := s.liveHostConsent(rec, s.chatTurn(ctx, p, conv)); c == nil || c.ID != id {
		return ErrHostExecExpired
	}
	// The answer goes to the record first, then on the bus: the waiting call
	// finds it on its next read even when the message is lost.
	again := false
	err = s.d.Store.UpdateHostConsent(ctx, p, conv, func(cur *session.HostConsent) (*session.HostConsent, error) {
		switch {
		case cur == nil || cur.ID != id:
			return cur, ErrHostExecExpired
		case cur.Decision == decision:
			again = true // a double click: it is applied, say so again
			return cur, nil
		case cur.Decision != "":
			return cur, ErrHostExecAnswered
		}
		cur.Decision, cur.AnsweredAt = decision, s.now().UTC()
		return cur, nil
	})
	if err != nil || again {
		return err
	}
	s.bus.Control(key(p, conv), ControlMsg{HostExec: &HostExecDecision{ID: id, Decision: decision}})
	return nil
}

// Builtins returns the registry of the built-in tools (bash, web_search,
// web_fetch), for Settings > Tools and for other features to register theirs.
func (s *Service) Builtins() *builtins.Registry { return s.builtins }

// builtinsFor builds the built-in tools of a chat.
func (s *Service) builtinsFor(ctx context.Context, a workspace.Actor, rec session.ChatRecord, subAgent bool) []builtins.Built {
	return s.builtins.Build(ctx, builtins.Chat{Actor: a, Origin: rec.Origin, Agent: rec.AgentID != "", AgentID: rec.AgentID, Model: rec.Model, SubAgent: subAgent,
		Admin: s.isAdmin(ctx, a.Principal), GroupsOff: rec.BuiltinsOff, GroupsOn: rec.BuiltinsOn})
}

// applyBuiltins adds the built-in tools that no add-on serves. When the
// built-in bash is added, the stand-in of an add-on that runs commands but
// cannot now is left out: bash serves instead, and the Tools menu says so.
func (s *Service) applyBuiltins(tools []session.Tool, built []builtins.Built) []session.Tool {
	var add []session.Tool
	for _, b := range built {
		if b.OK {
			add = append(add, b.Tool)
		}
	}
	tools = withBuiltins(tools, add...)
	if providerOf(tools, "bash") != BuiltinProvider {
		return tools
	}
	out := tools[:0:0]
	for _, t := range tools {
		if standInRunner([]session.Tool{t}) != "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// runsCommands reports whether a tool list has a tool that runs commands: an
// add-on's or the built-in bash.
func (s *Service) runsCommands(tools []session.Tool) bool {
	if addonRunner(tools) != "" {
		return true
	}
	return providerOf(tools, "bash") == BuiltinProvider
}

// SharedRoots lists the shared folders a user may use, for the scratch
// folder of the built-in bash.
func (s *Service) SharedRoots(ctx context.Context, a workspace.Actor) ([]session.SharedRoot, error) {
	return s.shared.Roots(ctx, a)
}
