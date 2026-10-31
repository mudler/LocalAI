package studio

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A chat that has no tool to run commands must say so. Without that fact a
// model writes a script, believes it ran, sends a sub-agent with the same
// tools to "really run it" and in the end prints a shell command as if it ran.
// So every turn gets one short block that says whether a tool runs commands,
// skills that need one are listed as unavailable, and a sub-agent task that
// asks to run something is refused at once.

// CommandIsolation is implemented by a broker that can tell how the tool that
// runs commands is isolated. It is optional.
type CommandIsolation interface {
	// Isolation is "isolated" when commands run in a sandbox, "host" when they
	// run directly on the server with no isolation, and "" when the broker
	// cannot tell.
	Isolation(ctx context.Context, user, addonID string) string
}

// CommandState says whether a chat can run commands.
type CommandState string

// The states. Only CommandsAvailable gives the model a tool that runs commands.
const (
	// CommandsAvailable: a tool of the chat runs commands.
	CommandsAvailable CommandState = "available"
	// CommandsOff: an add-on that runs commands is installed for the user, but
	// this chat switched it off, or the agent of the chat does not name it.
	CommandsOff CommandState = "off"
	// CommandsUnavailable: an add-on that runs commands is installed but cannot
	// run them on this server now (no backend works).
	CommandsUnavailable CommandState = "unavailable"
	// CommandsNone: no add-on that runs commands is installed for the user.
	CommandsNone CommandState = "none"
)

// Commands is what a chat knows about running commands at a turn.
type Commands struct {
	State CommandState
	// Tool is the name of the tool that runs commands, when State is available.
	Tool string
	// Isolation is "isolated", "host" or "" (unknown), when State is available.
	Isolation string
	// Reason is why the add-on cannot run commands, when State is unavailable.
	Reason string
	// Addon is the add-on that runs commands, or that would (off,
	// unavailable).
	Addon string
	// Provider serves the tool when State is available: the add-on's ID, or
	// BuiltinProvider for the built-in bash on this machine.
	Provider string
	// Consent is "ask" when each command of the built-in bash waits for the
	// person's yes.
	Consent string
	// Fallback is set when the built-in bash serves although an add-on that
	// runs commands is installed for the user: the add-on's ID, with
	// FallbackReason "off" (switched off for the chat or not named by its
	// agent) or why it cannot run.
	Fallback, FallbackReason string
}

// withHostBash is the state of a chat whose tools may hold the built-in bash:
// c is what the add-on tools alone gave.
func (s *Service) withHostBash(c Commands, tools []session.Tool) Commands {
	if c.State == CommandsAvailable {
		c.Provider = c.Addon
		return c
	}
	if providerOf(tools, "bash") != BuiltinProvider {
		return c
	}
	out := Commands{State: CommandsAvailable, Tool: "bash", Isolation: "host", Provider: BuiltinProvider}
	if s.hostExecMode() == "ask" {
		out.Consent = "ask"
	}
	switch c.State {
	case CommandsOff:
		out.Fallback, out.FallbackReason = c.Addon, "off"
	case CommandsUnavailable:
		out.Fallback, out.FallbackReason = c.Addon, c.Reason
		if out.FallbackReason == "" {
			out.FallbackReason = "it cannot run commands on this server now"
		}
	}
	return out
}

// skillNeedsExec is the reason Index gives for a skill that needs a tool that
// runs commands when the chat has none.
const skillNeedsExec = "needs command execution"

// runners remembers which add-on tools run commands, by tool name, and which
// add-ons have one. Tool names are the same for every user, so the cache is
// shared; it only grows with the set of installed add-ons, and every frontend
// fills its own from the broker at the first turn that lists the tools.
type runners struct {
	tools  sync.Map // tool name -> add-on ID
	addons sync.Map // add-on ID -> tool name
}

func (r *runners) mark(t AddonTool) {
	if t.Runs {
		r.tools.Store(t.Name, t.AddonID)
		r.addons.Store(t.AddonID, t.Name)
		if t.Shadows != "" {
			// In a chat the tool has the built-in's name: it is known by the
			// pair of its add-on and that name, never by the name alone,
			// which the built-in tool has too.
			r.tools.Store(shadowKey(t.AddonID, t.Shadows), t.AddonID)
		}
	}
}

func shadowKey(addon, name string) string { return addon + "\x00" + name }

// in returns the first tool of the list that an add-on serves and that runs
// commands, and its add-on. The built-in bash is not an add-on's: see
// builtinRunner.
func (r *runners) in(tools []session.Tool) (name, addon string) {
	for _, t := range tools {
		key := t.Name
		switch t.Provider {
		case BuiltinProvider:
			continue
		case "":
		default:
			key = shadowKey(t.Provider, t.Name)
		}
		if id, ok := r.tools.Load(key); ok {
			return t.Name, id.(string)
		}
	}
	return "", ""
}

// standIn returns the add-on whose stand-in tool (the add-on cannot work) is in
// the list, when that add-on runs commands.
func (r *runners) standIn(tools []session.Tool) string {
	for _, t := range tools {
		id, ok := strings.CutPrefix(t.Name, "addon__")
		if !ok {
			continue
		}
		id, ok = strings.CutSuffix(id, "__unavailable")
		if !ok {
			continue
		}
		if _, runs := r.addons.Load(id); runs {
			return id
		}
	}
	return ""
}

// commandsOf says whether the tools of a chat can run commands. addonsOff are
// the add-ons the chat switched off and sc the agent's scope, for a chat that
// cannot: they tell an add-on that is installed but off from none at all.
func (s *Service) commandsOf(ctx context.Context, user string, tools []session.Tool, addonsOff []string, sc *scope) Commands {
	if name, addon := s.runners.in(tools); name != "" {
		c := Commands{State: CommandsAvailable, Tool: name, Addon: addon}
		if iso, ok := s.d.AddonTools.(CommandIsolation); ok {
			c.Isolation = iso.Isolation(ctx, user, addon)
		}
		return c
	}
	if addon := s.runners.standIn(tools); addon != "" {
		c := Commands{State: CommandsUnavailable, Addon: addon}
		if gate, ok := s.d.AddonTools.(AddonGate); ok {
			c.Reason = gate.Unavailable(ctx, user, addon, s.d.Files.Available())
		}
		return c
	}
	off := set(addonsOff)
	found, which := CommandsNone, ""
	s.runners.addons.Range(func(k, _ any) bool {
		id := k.(string)
		if off[id] || (sc != nil && !sc.addons[id]) {
			found, which = CommandsOff, id
			return false
		}
		return true
	})
	if found == CommandsOff && !s.userHasRunner(ctx, user) {
		// Another user has the add-on; this one may not use it.
		found, which = CommandsNone, ""
	}
	return Commands{State: found, Addon: which}
}

// userHasRunner asks the broker whether the user may use an add-on that runs
// commands at all, whatever the chat switched.
func (s *Service) userHasRunner(ctx context.Context, user string) bool {
	if s.d.AddonTools == nil {
		return false
	}
	defs, err := s.d.AddonTools.Tools(ctx, user)
	if err != nil {
		return false
	}
	for _, d := range defs {
		if d.Runs {
			return true
		}
	}
	return false
}

// CommandRunner says whether a chat can run commands at its next turn, as the
// Tools menu shows it. It uses the same tools the model would get. The chat of
// another user is session.ErrNotFound.
func (s *Service) CommandRunner(ctx context.Context, user, conv string) (Commands, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return Commands{}, err
	}
	return s.commandsFor(ctx, p, rec), nil
}

// commandsFor says whether the chat of rec can run commands at its next turn.
func (s *Service) commandsFor(ctx context.Context, p string, rec session.ChatRecord) Commands {
	conv := rec.ID
	a := chatActor(p, conv)
	if rec.Kind == session.KindSubAgent {
		s.mu.Lock()
		tools := s.allowed[key(p, conv)]
		s.mu.Unlock()
		return s.commandsOf(ctx, p, tools, rec.AddonsOff, nil)
	}
	sc := s.scopeOf(ctx, rec)
	var tools []session.Tool
	if sc == nil || !sc.deleted {
		tools, _ = s.addonTools(ctx, a, rec, sc)
	}
	c := s.commandsOf(ctx, p, tools, rec.AddonsOff, sc)
	tools = s.applyBuiltins(tools, s.builtinsFor(ctx, a, rec, false))
	return s.withHostBash(c, tools)
}

// commandsBlock is the block of the system prompt that states whether the chat
// can run commands. writeFile says whether the chat has write_file, and
// skillsOff whether the skill index lists a skill as unavailable for want of a
// way to run commands. It is short on purpose: it goes into every turn.
func commandsBlock(c Commands, writeFile, skillsOff bool) string {
	if c.State == CommandsAvailable {
		switch {
		case c.Provider == BuiltinProvider:
			b := "Commands: " + c.Tool + " runs commands directly on this machine, with no isolation, in a scratch folder that holds the user's files: home/ there is /workspace/home of the file tools, shared-<name>/ is /workspace/shared-<name>. " +
				"It can read what the server can read and reach the internet, the local network and the LocalAI API. " +
				"Be careful: never delete or change anything outside the task, and never send the user's data anywhere, unless they asked."
			if c.Consent == "ask" {
				b += " Each command waits for the user's yes in the chat; if bash says it was not run, stop and ask them."
			}
			return b
		case c.Isolation == "isolated":
			return "Commands: " + c.Tool + " runs commands and code in an isolated sandbox."
		case c.Isolation == "host":
			return "Commands: " + c.Tool + " runs commands directly on the server, with no isolation. Ask before you delete or change anything outside the task."
		}
		return "Commands: " + c.Tool + " runs commands and code."
	}
	var b strings.Builder
	b.WriteString("Commands: nothing in this chat runs code or commands. Never claim or imply that you ran anything, and do not ask a sub-agent to run it: it has the same tools. Instead ")
	if writeFile {
		b.WriteString("write the output directly with write_file (text, CSV, Markdown, HTML, JSON) or ")
	}
	b.WriteString("give the user the script to run, and tell them ")
	switch c.State {
	case CommandsOff:
		b.WriteString("they can switch the Sandbox add-on on in the Tools menu of this chat.")
	case CommandsUnavailable:
		b.WriteString("the Sandbox add-on cannot run commands on this server; an administrator can fix that.")
	default:
		b.WriteString("an administrator can enable running code by installing the Sandbox add-on.")
	}
	if skillsOff {
		b.WriteString(" A skill marked unavailable here needs this: say so instead of starting it.")
	}
	return b.String()
}

// imagesBlock tells the model whether it sees the images the user attaches.
// A model that sees them must not send them to describe_image, which costs a
// second model call; a model that cannot must say so instead of guessing what
// a picture shows. describer is the model that describe_image would use, ""
// when there is none.
func imagesBlock(sees bool, describer string) string {
	switch {
	case sees:
		return "Images: you see the images attached to the user's message, with the message. " +
			"Answer about them directly. Use describe_image only for an image in the user's files that is not in the message."
	case describer != "":
		return "Images: you cannot see images. " +
			"To look at an image the user attached or has in their files, call describe_image with its path."
	}
	return "Images: you cannot see images, and no model here can describe them. " +
		"When the user shares an image, tell them plainly and suggest that they choose a model that can look at images for this chat."
}

// subAgentCommandsBlock is the block of a sub-agent that cannot run commands.
// Its parent decides what to tell the user.
const subAgentCommandsBlock = "Commands: nothing here runs code or commands. Never claim or imply that you ran anything; say plainly what you could not do."

// execGate returns the skill gate for a chat: a skill that requires a way to
// run commands is unavailable when the chat has none. n counts the skills it
// marked.
func execGate(c Commands, n *int) func(skills.Skill) string {
	return func(sk skills.Skill) string {
		if c.State == CommandsAvailable {
			return ""
		}
		for _, r := range sk.Requires {
			if r == skills.RequireExec {
				*n++
				return skillNeedsExec
			}
		}
		return ""
	}
}

// Sub-agent refusals for tasks that cannot work, returned to the model as the
// result of the tool call.
var (
	// ErrSubAgentCannotRun means the task asks to run something and nothing in
	// the chat runs commands: a sub-agent gets the same tools.
	ErrSubAgentCannotRun = errors.New("the sub-agent has no way to run commands either: nothing in this chat runs code or commands, and a sub-agent gets the same tools. " +
		"Do not try again. Write the output directly with a file tool, or give the user the script to run")
	// ErrSubAgentRepeat means a sub-agent already got the same task and no
	// file of the chat changed since, so it would answer the same.
	ErrSubAgentRepeat = errors.New("a sub-agent already got this exact task and no file of this chat changed since, so it would answer the same. " +
		"Use its answer, change the task, or tell the user what is missing")
)

// execTaskRE matches a task that asks to run something: "execute", "run the
// script", "run make_deck.py", "pip install". It is narrow on purpose: a
// research task that only names a language must still start.
var execTaskRE = regexp.MustCompile(`(?i)\bexecut(?:e|es|ed|ing|ion)\b|\brun(?:s|ning)?\s+it\b|\brun(?:s|ning)?\b[^.\n]{0,40}?(?:\b(?:script|command|commands|code|program|python3?|bash|shell|terminal)\b|\.(?:py|sh|js)\b)|\bpip3?\s+install\b|\bnpm\s+(?:install|run)\b`)

// asksToRun says whether a sub-agent task asks to run code or commands.
func asksToRun(task string) bool { return execTaskRE.MatchString(task) }

// normTask is a task as the repeat guard compares it: words, any case.
func normTask(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// repeatedTask says whether an earlier sub-agent of the chat got the same task
// and no file of the chat was created or changed since. It reads the store, so
// it holds when the turns of the chat run on different frontends.
func (s *Service) repeatedTask(ctx context.Context, a workspace.Actor, task string) (bool, error) {
	kids, err := s.d.Store.ListChildren(ctx, a.Principal, a.ChatID)
	if err != nil {
		return false, err
	}
	want := normTask(task)
	var last time.Time
	for _, k := range kids {
		es, err := s.d.Store.Entries(ctx, k.ID, 0, 1)
		if err != nil || len(es) == 0 {
			continue
		}
		if es[0].Role == session.RoleUser && normTask(es[0].Text) == want && es[0].At.After(last) {
			last = es[0].At
		}
	}
	if last.IsZero() {
		return false, nil
	}
	if !s.d.Files.Available() {
		return true, nil
	}
	cf, err := s.mgr.Files(ctx, a.Principal, a.ChatID)
	if err != nil {
		// Without the list nobody can tell: let the task run.
		return false, nil
	}
	for _, f := range cf.Files {
		if f.At.After(last) {
			return false, nil
		}
	}
	return true, nil
}
