package studio

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
	// Isolation is "isolated" when commands run in an isolated environment, "host" when they
	// run directly on the server with no isolation, and "" when the broker
	// cannot tell.
	Isolation(ctx context.Context, user, addonID string) string
}

// AddonControls is implemented by a broker that knows which host controls an
// add-on is entitled to (see contract.EntitledControls). It is optional.
type AddonControls interface {
	// Controls returns the host controls the host draws for the add-on, as
	// the apps list the user can read gives them, or nil.
	Controls(ctx context.Context, user, addonID string) []string
}

// AddonNetwork is the network an add-on declares for the commands it runs,
// as the host shows it (see contract.NetworkCapability).
type AddonNetwork struct {
	// Title names the network in text: the declared title, else the add-on's.
	Title string
	// Presets are the titles the add-on gives the host's network presets.
	Presets []contract.NetworkPreset
}

// AddonNetworks is implemented by a broker that knows the network each
// add-on declares. It is optional: an add-on it does not answer for gets the
// title of its commands tool.
type AddonNetworks interface {
	// Network returns the network of the add-on while the host draws its
	// network-policy control.
	Network(ctx context.Context, user, addonID string) (AddonNetwork, bool)
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
	// CommandsSwitchedOff: the chat switched the shell group off, so no tool
	// runs commands, whoever would serve them.
	CommandsSwitchedOff CommandState = "switched_off"
)

// Commands is what a chat knows about running commands at a turn.
type Commands struct {
	ExecutionStatus *ExecutionStatusView

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
	// Title names the add-on in the text the model and the person read: the
	// title its commands tool declares, or the add-on's own.
	Title string
	// Note is the sentence the add-on declares for the model when its commands
	// are isolated, or "".
	Note string
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
	// Draft is set when the chat does not exist yet. No execution status can be
	// read for it, so Isolation is unknown by construction, not by a failure.
	Draft bool
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

// commandTool is the tool name of the commands: the built-in bash, or the
// add-on tool that replaces it (contract.ToolDefinition shadows "bash").
const commandTool = "bash"

// addonRunner returns the add-on that serves the commands in the tool list:
// the provider of a bash that an add-on serves, or "". The built-in bash is
// not an add-on's: see providerOf.
func addonRunner(tools []session.Tool) string {
	for _, t := range tools {
		if t.Name == commandTool && t.Provider != "" && t.Provider != BuiltinProvider {
			return t.Provider
		}
	}
	return ""
}

// commandsDeclared is what the add-on that serves the commands in tools
// declares about its commands tool.
func commandsDeclared(tools []session.Tool, addon string) *contract.ExecutionDescriptor {
	for _, t := range tools {
		if t.Provider == addon && t.Execution != nil {
			return t.Execution
		}
	}
	return nil
}

// standInRunner returns the add-on whose stand-in tool (the add-on cannot
// work) is in the list, when that add-on replaces bash.
func standInRunner(tools []session.Tool) string {
	for _, t := range tools {
		if !slices.Contains(t.StandInFor, commandTool) {
			continue
		}
		id, tool, ok := addontools.ParseQualifiedToolName(t.Name)
		if ok && tool == "unavailable" {
			return id
		}
	}
	return ""
}

// commandAddons lists the installed add-ons that replace bash and that the
// user can see, oldest install first. With a broker that lists add-ons in any
// state it is every such add-on; else the running ones. It reads the broker
// each time, so every frontend answers the same, also after a restart.
func (s *Service) commandAddons(ctx context.Context, user string) []string {
	if s.d.AddonTools == nil {
		return nil
	}
	if _, ok := s.d.AddonTools.(ShadowLister); ok {
		sh := s.shadowersFor(ctx, user)
		return OrderProviders(sh.byName[commandTool], sh.when)
	}
	defs, err := s.d.AddonTools.Tools(ctx, user)
	if err != nil {
		return nil
	}
	var ids []string
	installed := map[string]string{}
	for _, d := range defs {
		if d.Shadows == commandTool && !slices.Contains(ids, d.AddonID) {
			ids = append(ids, d.AddonID)
			installed[d.AddonID] = d.InstalledAt
		}
	}
	return OrderProviders(ids, installed)
}

// commandTitle is the name of an add-on that is not serving commands now: the
// title of its manifest when the broker lists add-ons in any state, else the
// title its commands tool declares, else its ID.
func (s *Service) commandTitle(ctx context.Context, user, id string) string {
	if _, ok := s.d.AddonTools.(ShadowLister); ok {
		if t := s.shadowersFor(ctx, user).title[id]; t != "" {
			return t
		}
	}
	if s.d.AddonTools == nil {
		return id
	}
	if defs, err := s.d.AddonTools.Tools(ctx, user); err == nil {
		for _, d := range defs {
			if d.AddonID == id && d.Execution != nil && d.Execution.Title != "" {
				return d.Execution.Title
			}
		}
	}
	return id
}

// commandsOf says whether the tools of a chat can run commands. addonsOff are
// the add-ons the chat switched off and sc the agent's scope, for a chat that
// cannot: they tell an add-on that is installed but off from none at all.
func (s *Service) commandsOf(ctx context.Context, user string, tools []session.Tool, addonsOff []string, sc *scope) Commands {
	if addon := addonRunner(tools); addon != "" {
		c := Commands{State: CommandsAvailable, Tool: commandTool, Addon: addon, Title: addon}
		if e := commandsDeclared(tools, addon); e != nil {
			c.Note = e.Note
			if e.Title != "" {
				c.Title = e.Title
			}
		}
		if iso, ok := s.d.AddonTools.(CommandIsolation); ok {
			c.Isolation = iso.Isolation(ctx, user, addon)
		}
		return c
	}
	if addon := standInRunner(tools); addon != "" {
		c := Commands{State: CommandsUnavailable, Addon: addon, Title: s.commandTitle(ctx, user, addon)}
		if gate, ok := s.d.AddonTools.(AddonGate); ok {
			c.Reason = gate.Unavailable(ctx, user, addon, s.d.Files.Available())
		}
		return c
	}
	off := set(addonsOff)
	for _, id := range s.commandAddons(ctx, user) {
		if off[id] || (sc != nil && !sc.addons[id]) {
			return Commands{State: CommandsOff, Addon: id, Title: s.commandTitle(ctx, user, id)}
		}
	}
	return Commands{State: CommandsNone}
}

// CommandRunner says whether a chat can run commands at its next turn, as the
// Tools menu shows it. It uses the same tools the model would get. The chat of
// another user is session.ErrNotFound.
func (s *Service) CommandRunner(ctx context.Context, user, conv string) (Commands, error) {
	ctx, closeScope := s.executionScope(ctx, principal(user), conv)
	defer closeScope()
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return Commands{}, err
	}
	return s.commandsFor(ctx, p, rec)
}

// commandsFor says whether the chat of rec can run commands at its next turn.
func (s *Service) commandsFor(ctx context.Context, p string, rec session.ChatRecord) (Commands, error) {
	conv := rec.ID
	ctx, closeScope := s.executionScope(ctx, p, conv)
	defer closeScope()
	a := s.chatActorFor(ctx, rec)
	if rec.Kind == session.KindSubAgent && rec.AgentID == "" {
		s.mu.Lock()
		tools := s.allowed[key(p, conv)]
		s.mu.Unlock()
		return s.commandsOf(ctx, p, tools, rec.AddonsOff, nil), nil
	}
	sc := s.scopeOf(ctx, rec)
	var tools []session.Tool
	if sc == nil || !sc.deleted {
		var err error
		tools, _, err = s.addonTools(ctx, a, rec, sc)
		if err != nil {
			return Commands{}, err
		}
	}
	c := s.commandsOf(ctx, p, tools, rec.AddonsOff, sc)
	c.Draft = rec.ID == ""
	if _, ok := s.d.AddonTools.(ExecutionObserver); ok {
		c.ExecutionStatus = executionStatusView(ctx, p, c.Addon)
	}
	tools = s.applyBuiltins(tools, s.builtinsFor(ctx, a, rec, false))
	return shellSwitched(s.withHostBash(c, tools), s.hiddenFor(ctx, rec)), nil
}

// commandsWhere is where a command of an add-on starts and what it keeps.
// fileTools says whether the chat has the file tools, which see the same
// files: a chat without them must not hear of them. A scoped chat (its agent
// works in one folder) has that folder at /workspace/home, at the agent's
// mode, and its chat folder at /workspace/chat; its shared folders are read
// only.
func commandsWhere(fileTools bool, place session.Place) string {
	same := ""
	if fileTools {
		same = ", the same files as the file tools"
	}
	if place.Scoped() {
		kept := "/workspace/chat"
		if place.PrimaryWritable() {
			kept = "/workspace/home or /workspace/chat"
		}
		return "It starts in /workspace/home, the agent's folder" + same + "; the chat's own folder is /workspace/chat, and shared folders are read only. Only what it saves under " + kept + " is kept."
	}
	return "It starts in /workspace/home, the user's home" + same + "; only what it saves under /workspace/home or a writable shared folder is kept."
}

// skillsNotInProvider is the sentence that keeps a model from looking for a
// skill folder in an isolated environment, which sees the user's home only, and
// points it at the tool that loads a skill by its name.
const skillsNotInProvider = "Skill folders are not visible in the isolated environment: load skills with load_skill."

// commandsTitle is the name the prompt gives the add-on that runs commands.
func commandsTitle(c Commands) string {
	if c.Title != "" {
		return c.Title
	}
	return "the add-on"
}

// commandsPhrase names the add-on that runs commands in a sentence: the
// declared title when there is one, else a generic description. The title comes
// from the add-on's declaration, never from this file.
func commandsPhrase(c Commands) string {
	if c.Title != "" {
		return "the " + c.Title + " add-on"
	}
	return "the add-on that runs commands"
}

// commandsBlock is the block of the system prompt that states whether the chat
// can run commands. writeFile says whether the chat has write_file,
// fileTools whether it has the file tools to read them, and skillsOff
// whether the skill index lists a skill as unavailable for want of a way to
// run commands, and place the chat's folders. It is short on purpose: it
// goes into every turn.
func commandsBlock(c Commands, writeFile, fileTools, skillsOff bool, place session.Place) string {
	if c.State == CommandsAvailable {
		home := "the user's home files"
		if fileTools {
			home = "the files of /workspace/home of the file tools"
		}
		switch {
		case c.Provider == BuiltinProvider:
			b := "Commands: " + c.Tool + " runs commands directly on this machine, with no isolation. It starts in the user's home inside a scratch folder (" + home + "; a shared folder is ../shared-<name>), so use relative paths there. " +
				"It can read what the server can read and reach the internet, the local network and the LocalAI API. " +
				"Be careful: never delete or change anything outside the task, and never send the user's data anywhere, unless they asked."
			if c.Consent == "ask" {
				b += " Each command waits for the user's yes in the chat; if bash says it was not run, stop and ask them."
			}
			return b
		case c.Isolation == "isolated":
			// The turn's note is cut at 400 characters and the tool description
			// is capped, so the list of programs goes here, in the prompt.
			b := "Commands: " + c.Tool + " runs commands and code in an isolated environment (" + commandsTitle(c) + "). " + commandsWhere(fileTools, place) + " "
			if c.Note != "" {
				b += c.Note + " "
			}
			return b + skillsNotInProvider
		case c.Isolation == "host":
			return "Commands: " + c.Tool + " runs commands directly on the server, with no isolation. Ask before you delete or change anything outside the task. " + commandsWhere(fileTools, place)
		}
		return "Commands: " + c.Tool + " runs commands and code. " + commandsWhere(fileTools, place)
	}
	var b strings.Builder
	b.WriteString("Commands: nothing in this chat runs code or commands. Never claim or imply that you ran anything, and do not ask a sub-agent to run it: it has the same tools. Instead ")
	if writeFile {
		b.WriteString("write the output directly with write_file (text, CSV, Markdown, HTML, JSON) or ")
	}
	b.WriteString("give the user the script to run, and tell them ")
	switch c.State {
	case CommandsSwitchedOff:
		b.WriteString("they switched running commands off for this chat and can switch Run commands on in its Tools menu.")
	case CommandsOff:
		b.WriteString("they can switch " + commandsPhrase(c) + " on in the Tools menu of this chat.")
	case CommandsUnavailable:
		b.WriteString(commandsPhrase(c) + " cannot run commands on this server; an administrator can fix that.")
	default:
		b.WriteString("an administrator can enable running code by installing an add-on that runs commands.")
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
// when there is none, and describe says whether the chat has describe_image:
// a chat that switched the image helpers off must not hear of the tool.
func imagesBlock(sees bool, describer string, describe bool) string {
	switch {
	case sees && describe:
		return "Images: you see the images attached to the user's message, with the message. " +
			"Answer about them directly. Use describe_image only for an image in the user's files that is not in the message."
	case sees:
		return "Images: you see the images attached to the user's message, with the message. Answer about them directly."
	case describer != "" && describe:
		return "Images: you cannot see images. " +
			"To look at an image the user attached or has in their files, call describe_image with its path."
	case describer != "":
		return "Images: you cannot see images, and the image helper is switched off for this chat. " +
			"When the user shares an image, tell them plainly; they can switch Images and audio on in the Tools menu of this chat."
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
		if slices.Contains(sk.Requires, skills.RequireExec) {
			*n++
			return skillNeedsExec
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

// isolatedProvider reports whether the add-on serves the chat's commands and
// the typed execution status says they are isolated. It is what the skills of
// that add-on that need isolation require: the selected command provider, not
// just an installed guest or generic command authority. Unknown isolation
// fails closed.
func isolatedProvider(c Commands, addon string) bool {
	return c.State == CommandsAvailable && c.Isolation == "isolated" && c.Addon == addon && c.Provider == addon
}

// addonCommandDescriptor describes existing add-on authority without building
// built-ins (which build load_skill) or invoking any tool callbacks.
func (s *Service) addonCommandDescriptor(ctx context.Context, p string, rec session.ChatRecord, sc *scope) (Commands, error) {
	access, err := s.AddonAccess(ctx, p, rec.ID)
	if err != nil {
		return Commands{State: CommandsNone}, err
	}
	return s.addonCommandDescriptorForActor(ctx, access.Actor(), rec, sc)
}

// addonCommandDescriptorForActor reuses the actor already authorized by turn
// setup (or the tools panel). It must not resolve session access: tool building
// can run while Manager.Open holds its open lock.
func (s *Service) addonCommandDescriptorForActor(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope) (Commands, error) {
	p := a.Principal
	if sc != nil && sc.deleted {
		return Commands{State: CommandsNone}, nil
	}
	ctx, closeScope := s.executionScope(ctx, p, rec.ID)
	defer closeScope()
	var tools []session.Tool
	// Only ad-hoc children inherit a pruned tool set. Saved-agent children
	// resolve current grants through their scope, just like their turn tools.
	if rec.Kind == session.KindSubAgent && rec.AgentID == "" {
		s.mu.Lock()
		tools = slices.Clone(s.allowed[key(p, rec.ID)])
		s.mu.Unlock()
	} else {
		var err error
		tools, _, err = s.addonTools(ctx, a, rec, sc)
		if err != nil {
			return Commands{}, err
		}
	}
	c := s.commandsOf(ctx, p, tools, rec.AddonsOff, sc)
	c.Draft = rec.ID == ""
	if c.State == CommandsAvailable {
		c.Provider = c.Addon
	}
	return shellSwitched(c, s.hiddenFor(ctx, rec)), nil
}
