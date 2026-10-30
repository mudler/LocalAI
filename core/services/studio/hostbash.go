package studio

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/session"
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

// hostExecWait is how long a command waits for the card. A reload loses the
// card; the call then ends with "no answer".
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
	run := t.Run
	a := c.Actor
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
// or stopped when the turn ends first.
func (s *Service) askHostExec(ctx context.Context, a workspace.Actor, command string) string {
	id := newID()
	k := key(a.Principal, a.ChatID)
	answer := make(chan string, 1)
	cancel := s.bus.OnControl(k, func(m ControlMsg) {
		if m.HostExec != nil && m.HostExec.ID == id {
			select {
			case answer <- m.HostExec.Decision:
			default:
			}
		}
	})
	defer cancel()
	if len(command) > 2000 {
		command = strings.ToValidUTF8(command[:2000], "") + "..."
	}
	s.bus.Publish(k, session.Event{Kind: session.KindHostExecConsent, IDs: []string{id}, Text: command})
	t := time.NewTimer(hostExecWait)
	defer t.Stop()
	select {
	case d := <-answer:
		return d
	case <-t.C:
		return "timeout"
	case <-ctx.Done():
		return "stopped"
	}
}

// HostExecDecide answers the consent card of a chat. Only an administrator
// may allow a command; anyone who owns the chat may refuse it. The chat of
// another user is session.ErrNotFound.
func (s *Service) HostExecDecide(ctx context.Context, user, conv, id, decision string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
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
	s.bus.Control(key(p, conv), ControlMsg{HostExec: &HostExecDecision{ID: id, Decision: decision}})
	return nil
}

// Builtins returns the registry of the built-in tools (bash, web_search,
// web_fetch), for Settings > Tools and for other features to register theirs.
func (s *Service) Builtins() *builtins.Registry { return s.builtins }

// builtinsFor builds the built-in tools of a chat.
func (s *Service) builtinsFor(ctx context.Context, a workspace.Actor, rec session.ChatRecord, subAgent bool) []builtins.Built {
	return s.builtins.Build(ctx, builtins.Chat{Actor: a, Origin: rec.Origin, Agent: rec.AgentID != "", SubAgent: subAgent,
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
		if s.runners.standIn([]session.Tool{t}) != "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// runsCommands reports whether a tool list has a tool that runs commands: an
// add-on's or the built-in bash.
func (s *Service) runsCommands(tools []session.Tool) bool {
	if name, _ := s.runners.in(tools); name != "" {
		return true
	}
	return providerOf(tools, "bash") == BuiltinProvider
}

// SharedRoots lists the shared folders a user may use, for the scratch
// folder of the built-in bash.
func (s *Service) SharedRoots(ctx context.Context, a workspace.Actor) ([]session.SharedRoot, error) {
	return s.shared.Roots(ctx, a)
}
