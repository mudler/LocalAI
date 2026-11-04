package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// maxBackgroundChildren is how many sub-agents one conversation may run in the
// background at once.
const maxBackgroundChildren = 4

// Sub-agent errors, returned to the model as the result of the tool call.
var (
	// ErrSubAgentDepth means a sub-agent tried to start another. Sub-agents
	// nest one level only.
	ErrSubAgentDepth = errors.New("a sub-agent cannot start another sub-agent")
	// ErrSubAgentLimit means the conversation already runs the most background
	// sub-agents it may.
	ErrSubAgentLimit = errors.New("the limit of background sub-agents for this conversation is reached; wait for one to finish")
	// errNoTurnAccess means the hook got an access that has no folder: the
	// chat's access was not resolved, so it gets no tools.
	// ErrAgentOutsideFolder means spawn_agent named a saved agent whose folder
	// is not the folder of the chat's own agent or inside it. A sub-agent never
	// has more reach than its parent, so the agent is refused, not widened.
	ErrAgentOutsideFolder = errors.New("this agent works in another folder than this chat")
	errNoTurnAccess       = errors.New("studio: the chat's access was not resolved; no tools this turn")
)

type child struct {
	id         string
	title      string
	background bool
}

// chatTools is the hook that gives every chat its host tools. A conversation
// gets the host tools plus spawn_agent; a sub-agent gets only the tools its
// parent chose and never spawn_agent. A sub-agent started from a saved agent
// gets what the agent grants instead, read from the store like any chat with
// an agent, so it needs no tool list in memory. Every tool is built from the
// chat's access for the turn: its actor is fenced when the chat's agent has a
// folder.
func (s *Service) chatTools(ctx context.Context, x session.Access) (session.TurnTools, error) {
	a := x.Actor()
	if s.d.Files.Available() && x.Place().Primary() == "" {
		// An access without a folder was never resolved for this chat
		// (chatAccess gives every chat at least its home or its agent's
		// folder). Refuse the tools rather than build them on a guess.
		return session.TurnTools{}, errNoTurnAccess
	}
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return session.TurnTools{}, err
	}
	sub := rec.Kind == session.KindSubAgent
	if sub && rec.AgentID == "" {
		s.mu.Lock()
		tools := s.allowed[key(a.Principal, a.ChatID)]
		s.mu.Unlock()
		var prompt, work string
		cmds := s.withHostBash(s.commandsOf(ctx, a.Principal, tools, nil, nil), tools)
		if cmds.State != CommandsAvailable {
			prompt = subAgentCommandsBlock
		}
		if canWriteFiles(tools, cmds) {
			work = s.subWorkBlock(ctx, rec, x)
		}
		prompt = joinBlocks(prompt, webBlock(tools), work)
		return session.TurnTools{Tools: tools, Prompt: prompt}, nil
	}
	tools, missing, err := s.scopedHostTools(ctx, a)
	if err != nil {
		var policyErr *providerPolicyError
		if errors.As(err, &policyErr) {
			return session.TurnTools{}, err
		}
		s.log.Warn("studio: host tools unavailable", "chat", a.ChatID, "error", err)
	}
	sc := s.scopeOf(ctx, rec)
	// The tools the chat's switches take away (a sub-agent follows its
	// parent's): scopedHostTools dropped the add-on ones already.
	hidden := s.hiddenFor(ctx, rec)
	cmds := s.commandsOf(ctx, a.Principal, tools, rec.AddonsOff, sc)
	// The built-in tools no add-on serves: bash on this machine when the
	// Sandbox does not serve it and the setting allows it.
	tools = s.applyBuiltins(tools, s.builtinsFor(ctx, a, rec, sub))
	cmds = shellSwitched(s.withHostBash(cmds, tools), hidden)
	// The file tools are the one file toolset of the chat. A sub-agent gets
	// them from subset, built for the parent chat; one from a saved agent gets
	// them here, built for the parent chat too, so the parent's Files pane
	// sees its changes.
	// Every file and media tool takes the access of this turn, resolved
	// once by chatAccess: none builds a view of its own.
	fx := x
	if sub {
		if fx, err = x.ForChat(rec.ParentID); err != nil {
			return session.TurnTools{}, err
		}
	}
	fa := fx.Actor()
	nudge := s.newRulesNudge(ctx, x, rec)
	tools = append(tools, nudge.wrap(s.fileNudgeTools(fx, s.announceFileChanges(fa, s.skillHintTools(fa, s.binaryHintTools(fa, cmds.State == CommandsAvailable, dropHidden(s.d.Files.Tools(fx), hidden))))))...)
	if !sub {
		tools = append(tools, s.attachTools(ctx, fx, rec)...)
	}
	if s.d.Media != nil {
		tools = append(tools, s.skillHintTools(fa, dropHidden(s.d.Media.Tools(s.d.Files, x, rec.Model), hidden))...)
	}
	// What the prompt may tell the model to do with the files follows the
	// tools it has now: a chat with the file tools switched off hears of none.
	canRead, canWrite := canReadFiles(tools, cmds), canWriteFiles(tools, cmds)
	skillsOff := 0
	prompt, note, key, fallback, err := s.skillTurn(ctx, a.Principal, rec.SkillsOff, rec.AddonsOff, sc, cmds, execGate(cmds, &skillsOff))
	if err != nil {
		return session.TurnTools{}, err
	}
	if !canWrite {
		prompt = withoutWriteHint(prompt)
	}
	// The persona and the facts about the user come after the chat's prompt
	// and the agent's instructions, which take precedence, and before the
	// index of skills. A change to either file restarts the harness at the
	// next turn, because the prompt changes.
	var persona, personaKey string
	if !sub {
		persona, personaKey = s.WellKnown().TurnPrompt(ctx, a.Principal)
	}
	// The agent's profile and the index of the user's notes follow the persona.
	// The first is the user's standing rules for this agent; the second points
	// the model at context_read.
	var notes, notesKey string
	if !sub {
		extra := s.addonContextSources(ctx, a.Principal, rec.AddonsOff, sc)
		notes, notesKey = s.contextTurn(ctx, rec, tools, extra)
		tools = append(tools, s.contextTools(rec, tools, extra)...)
	}
	if lead := joinBlocks(persona, notes); lead != "" {
		prompt = strings.TrimSpace(lead + "\n\n" + prompt)
	}
	if sc != nil {
		// Granted skills the user lacks come back from skillTurn as a note of
		// their own; fold all the missing grants into one line.
		var skillMissing []string
		if note != "" {
			skillMissing = strings.Split(note, "; ")
		}
		note = unavailable(missing, skillMissing)
		if sc.instructions != "" {
			// The instructions go first: the prompt is cut at its end.
			prompt = strings.TrimSpace(sc.instructions + "\n\n" + prompt)
		}
	}
	if fallback != "" {
		note = strings.TrimSpace(note + " " + fallback)
	}
	if !sub && !hidden[spawnAgentName] {
		// The agent argument is offered only when list_agents is: the chat
		// switched the saved agents on and its user may use them.
		tools = append(tools, s.spawnTool(x, hasTool(tools, listAgentsName)))
	}
	// The assistant tools are not part of hostTools, so a sub-agent never gets
	// them, whatever its parent names.
	aTools, aPrompt, aNote := s.assistantTurn(ctx, rec)
	tools = append(tools, aTools...)
	// The tools of a self-paced loop, while the chat has one.
	tools = append(tools, s.loopTools(ctx, a, rec)...)
	if aPrompt != "" {
		prompt = strings.TrimSpace(aPrompt + "\n\n" + prompt)
	}
	if aNote != "" {
		note = strings.TrimSpace(note + " " + aNote)
	}
	// What the user did with the triggers this chat proposed: the tool result
	// said they were waiting, so a later turn must hear how they ended.
	if s.triggers != nil {
		if line := s.triggers.ProposalNote(ctx, a.Principal, a.ChatID); line != "" {
			note = strings.TrimSpace(note + " " + line)
		}
	}
	// An offer from the assistant is allowed in few turns (see hintsvc.go); the
	// model learns it from one line of the note, so other turns cost nothing.
	if line := s.hintLine(ctx, a, rec); line != "" {
		note = strings.TrimSpace(line + " " + note)
	}
	// The network of the commands an add-on runs, in one line: it follows the
	// chat's preset at each turn and needs no restart of the harness.
	if cmds.State == CommandsAvailable && cmds.Isolation == "isolated" && cmds.Provider != BuiltinProvider {
		line, err := s.egressLine(ctx, rec, hasTool(tools, webSearchName) || hasTool(tools, webFetchName), s.networkOf(ctx, a.Principal, cmds).Title)
		if err != nil {
			return session.TurnTools{}, err
		}
		note = strings.TrimSpace(line + " " + note)
	}
	// The AGENTS.md files of the chat's folder and the home: a pointer, first,
	// so the cut of a long note keeps it. A chat that cannot read them is
	// not told to.
	if canRead {
		if line := nudge.turnLine(ctx, rec); line != "" {
			note = strings.TrimSpace(line + " " + note)
		}
	}
	// What the chat can run comes before the other blocks: the prompt is cut at
	// its end, and this block must reach the model.
	// What the chat can see follows it, for the same reason. Images reach a
	// chat only as files, so a chat without files has no such block.
	head := commandsBlock(cmds, hasTool(tools, "write_file"), hasTool(tools, "read_file"), skillsOff > 0, x.Place())
	if s.d.Files.Available() {
		sees := s.d.Modalities != nil && s.d.Modalities(rec.Model).Vision
		head += "\n\n" + imagesBlock(sees, s.d.Media.VisionModel(rec.Model), hasTool(tools, "describe_image"))
	}
	var goal string
	if !sub && goalActive(rec) {
		// The goal comes right after what the chat can run: the prompt is cut
		// at its end, and the model must not lose the goal.
		goal = goalBlock(rec.Goal)
	}
	prompt = joinBlocks(head, goal, s.connectorBlock(ctx, rec, canWrite), webBlock(tools), prompt)
	if sp := strings.TrimSpace(rec.Settings.SystemPrompt); sp != "" {
		// The chat's own instructions come first: the prompt is cut at its end.
		prompt = strings.TrimSpace(sp + "\n\n" + prompt)
	}
	// The files go last: the prompt is cut at its end, and a block that
	// changes between turns leaves the start of the prompt as it was.
	if fp := s.filesPrompt(ctx, x); fp != "" {
		if !canRead {
			fp += "\n" + filesNote
		}
		prompt = strings.TrimSpace(prompt + "\n\n" + fp)
	}
	// A conversation names its own work folder (the session takes the folder
	// name); a sub-agent saves in its parent's files, so it names the parent's.
	// A chat that cannot save a file has no use for one.
	work := !sub && s.workFolderOn() && s.d.Files.Available() && canWrite
	if sub && canWrite {
		prompt = joinBlocks(prompt, s.subWorkBlock(ctx, rec, x))
	}
	// load_skill learns from the final list whether the chat can read a
	// skill's files and run what a skill needs.
	tools = s.fitLoadSkill(a, tools)
	// The action policy of a run that nobody watches: the tools that change
	// files or run commands on this machine are checked at each call.
	tools = s.gateBuiltinActs(ctx, rec, tools)
	return session.TurnTools{Tools: tools, Prompt: prompt, Note: note, WorkFolder: work, RefreshKey: key + "\x00" + rec.Settings.SystemPrompt + "\x00" + personaKey + "\x00" + notesKey}, nil
}

// spawnTool is spawn_agent for the chat of x: a generic sub-agent gets
// tools built from x, so it reaches what the parent's turn reaches.
func (s *Service) spawnTool(x session.Access, savedAgents bool) session.Tool {
	desc := "Start a sub-agent that works on a task and reports back. Use it for a self-contained piece of work. " +
		"By default you wait for its answer. With wait=false it runs in the background and its result arrives later as a message."
	agent := ""
	if savedAgents {
		desc += " With agent, the sub-agent is one of the user's saved agents (see " + listAgentsName + "): it works with that agent's instructions, model and tools."
		agent = `"agent":{"type":"string","description":"The id or name of a saved agent from ` + listAgentsName + `. Omit for a plain sub-agent with your tools. Cannot be combined with tools."},`
	}
	return session.Tool{
		Name:        "spawn_agent",
		Description: desc,
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"task":{"type":"string","description":"What the sub-agent should do, with everything it needs to know."},` +
			agent +
			`"tools":{"type":"array","items":{"type":"string"},"description":"Names of your tools the sub-agent may use. Omit to give it all of them; [] gives it none."},` +
			`"wait":{"type":"boolean","description":"true (default): wait for the answer. false: carry on now."}},` +
			`"required":["task"]}`),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return s.spawn(ctx, x, raw) },
	}
}

func (s *Service) spawn(ctx context.Context, x session.Access, raw json.RawMessage) (string, error) {
	a := x.Actor()
	var args struct {
		Task  string    `json:"task"`
		Agent string    `json:"agent"`
		Tools *[]string `json:"tools"`
		Wait  *bool     `json:"wait"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	task := strings.TrimSpace(args.Task)
	if task == "" {
		return "", errors.New("task is required")
	}
	wait := args.Wait == nil || *args.Wait
	parent, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return "", err
	}
	if parent.Kind == session.KindSubAgent {
		return "", ErrSubAgentDepth
	}
	if s.hiddenFor(ctx, parent)[spawnAgentName] {
		return "", ErrSubAgentsOff
	}

	var (
		subset []session.Tool
		agent  agents.Agent
	)
	if ref := strings.TrimSpace(args.Agent); ref != "" {
		// A saved agent: the permission is checked again at the call, since
		// the setting, the chat's switch or the user's feature may have
		// changed since the turn started.
		if args.Tools != nil {
			return "", ErrSavedAgentWithTools
		}
		if err := s.savedAgentsAllowed(ctx, parent); err != nil {
			return "", err
		}
		if unattended(parent.Origin) {
			// The chat of a saved agent has no origin, so it would not carry the
			// rights of this run.
			return "", ErrSavedAgentUnattended
		}
		if agent, err = s.resolveAgent(ctx, a.Principal, ref); err != nil {
			return "", err
		}
		if err := s.agentFitsChat(ctx, parent, agent); err != nil {
			return "", err
		}
	} else {
		if subset, err = s.subset(ctx, x, args.Tools); err != nil {
			return "", err
		}
		// A sub-agent has the parent's tools or fewer: when none of them runs
		// commands, a task that asks to run something cannot work. Say so now
		// instead of spending a turn of the sub-agent on it.
		if !s.runsCommands(subset) && asksToRun(task) {
			return "", ErrSubAgentCannotRun
		}
	}
	if again, err := s.repeatedTask(ctx, a, task); err != nil {
		return "", err
	} else if again {
		return "", ErrSubAgentRepeat
	}

	parentKey := key(a.Principal, a.ChatID)
	childID := newID()
	title := "Sub-agent: " + truncate(task, 60)
	model := parent.Model
	if agent.ID != "" {
		title = "Sub-agent " + agent.Name + ": " + truncate(task, 60)
		if agent.Model != "" {
			model = agent.Model
		}
	}
	if err := s.registerChild(ctx, a.Principal, a.ChatID, &child{id: childID, title: title, background: !wait}); err != nil {
		return "", err
	}
	now := parent.Updated
	rec := session.ChatRecord{ID: childID, Principal: a.Principal, Title: title, Created: now, Updated: now,
		Kind: session.KindSubAgent, ParentID: a.ChatID, Model: model, AgentID: agent.ID,
		// The connections the parent chose are the child's: its tools run with
		// the same credentials, and it chooses none of its own.
		Connections: connsets.Clone(parent.Connections),
		// The child is refused, never given the whole home, when the folder
		// it works in can no longer be found (chatScopes).
		ScopedAgent: parent.ScopedAgent || agent.HasFolder()}
	if err := s.d.Store.CreateChat(ctx, rec); err != nil {
		s.forgetChild(parentKey, childID)
		return "", err
	}
	s.bus.Publish(parentKey, session.Event{Kind: session.KindSubAgentStarted, IDs: []string{childID}, Text: title})
	childKey := key(a.Principal, childID)
	if agent.ID == "" {
		s.mu.Lock()
		s.allowed[childKey] = s.fitLoadSkill(a, subset)
		s.mu.Unlock()
	}
	done := s.waitFor(childKey)
	if _, err := s.send(context.WithoutCancel(ctx), a.Principal, childID, session.Message{Text: task}); err != nil {
		// The child never ran: drop what was set up for it, or the waiter and
		// an empty sub-agent conversation would stay behind.
		s.forgetWaiter(childKey, done)
		s.forgetChild(parentKey, childID)
		if derr := s.d.Store.DeleteChat(context.WithoutCancel(ctx), a.Principal, childID); derr != nil {
			s.log.Warn("studio: could not remove a sub-agent that did not start", "chat", childID, "error", derr)
		}
		return "", err
	}

	if !wait {
		go s.reportBack(a, childID, title, done) // #nosec G118 -- a background sub-agent reports after the request ended
		return fmt.Sprintf("Started sub-agent %s in the background. Its result will arrive as a message.", childID), nil
	}
	select {
	case st := <-done:
		s.forgetChild(parentKey, childID)
		return s.childResult(ctx, a, childID, st)
	case <-ctx.Done():
		_ = s.Stop(context.WithoutCancel(ctx), a.Principal, childID)
		s.forgetChild(parentKey, childID)
		return "", ctx.Err()
	}
}

// agentFitsChat checks that a saved agent may run as a sub-agent of the
// chat: when the chat's own agent has a folder, the agent has none or has one
// that is the same or inside it. The check is made from the stores at the
// call, before any chat is made for the child.
func (s *Service) agentFitsChat(ctx context.Context, chat session.ChatRecord, ag agents.Agent) error {
	outer, err := s.agentFolderScope(ctx, chat, false)
	if err != nil {
		return err
	}
	if !s.agentFitsScope(ctx, chat.Principal, outer, ag) {
		return ErrAgentOutsideFolder
	}
	return nil
}

// agentFitsScope says whether ag may work under outer, the folder of the
// chat's agent (nil for a chat with none). An agent whose folder cannot be
// found does not fit a folder: nothing says it is inside.
func (s *Service) agentFitsScope(ctx context.Context, p string, outer *folderScope, ag agents.Agent) bool {
	if outer == nil || !ag.HasFolder() {
		return true
	}
	inner, err := s.liveFolder(ctx, p, ag)
	if err != nil {
		return false
	}
	// Every folder of the sub-agent is the same as, or inside, one folder of
	// the chat's agent.
	for _, f := range inner.folders {
		if !outer.contains(f.path) {
			return false
		}
	}
	return true
}

// subset picks the tools a sub-agent gets from the parent's host tools and
// file tools, built from x, the parent's access for this turn. Nil asks for
// all of them but the named-only ones; a name the parent does not have is an
// error.
func (s *Service) subset(ctx context.Context, x session.Access, names *[]string) ([]session.Tool, error) {
	a := x.Actor()
	have, err := s.hostTools(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("could not list your tools: %w", err)
	}
	// The built-in file tools act for the parent chat: what a sub-agent changes
	// is an agent change of the parent, and the parent's Files pane refreshes.
	parent, perr := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	fileTools := s.d.Files.Tools(x)
	if perr == nil {
		// A sub-agent never gets a tool its parent switched off.
		fileTools = dropHidden(fileTools, s.hiddenFor(ctx, parent))
	}
	have = append(have, s.fileNudgeTools(x, s.announceFileChanges(a, s.skillHintTools(a, fileTools)))...)
	if perr == nil {
		// Built for a sub-agent: bash on this machine only when no card is
		// needed, since nobody watches the sub-agent's chat.
		have = s.applyBuiltins(have, s.builtinsFor(ctx, a, parent, true))
		// A sub-agent of a run that nobody watches has the rights of that run.
		have = s.gateBuiltinActs(ctx, parent, have)
	}
	if names == nil {
		kept := have[:0:0]
		for _, t := range have {
			if !namedOnlyTools[t.Name] {
				kept = append(kept, t)
			}
		}
		return kept, nil
	}
	byName := make(map[string]session.Tool, len(have))
	for _, t := range have {
		byName[t.Name] = t
	}
	out := make([]session.Tool, 0, len(*names))
	for _, n := range *names {
		t, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("the tool %q is not one of your tools, so a sub-agent cannot have it", n)
		}
		out = append(out, t)
	}
	return out, nil
}

// registerChild records a running sub-agent, refusing a background one past
// the limit. The limit counts the parent's running sub-agents in the store, so
// it holds across frontends: a sub-agent that a turn on another frontend
// started still counts. Children registered here but not yet running count too.
func (s *Service) registerChild(ctx context.Context, p, conv string, c *child) error {
	parentKey := key(p, conv)
	running := map[string]bool{}
	if c.background {
		kids, err := s.runningChildren(ctx, p, conv)
		if err != nil {
			return err
		}
		for _, k := range kids {
			running[k.ID] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.background {
		n := len(running)
		for id, k := range s.children[parentKey] {
			if k.background && !running[id] {
				n++
			}
		}
		if n >= maxBackgroundChildren {
			return ErrSubAgentLimit
		}
	}
	if s.children[parentKey] == nil {
		s.children[parentKey] = map[string]*child{}
	}
	s.children[parentKey][c.id] = c
	return nil
}

// runningChildren returns the sub-agent chats of a conversation whose turn
// runs now, on any frontend.
func (s *Service) runningChildren(ctx context.Context, p, conv string) ([]session.ChatRecord, error) {
	kids, err := s.d.Store.ListChildren(ctx, p, conv)
	if err != nil {
		return nil, err
	}
	var out []session.ChatRecord
	for _, k := range kids {
		info, err := s.d.Store.Turn(ctx, p, k.ID)
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				continue // deleted meanwhile
			}
			return nil, err
		}
		if info.Running {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *Service) forgetChild(parentKey, id string) {
	s.mu.Lock()
	// The child's tool list is needed only while its turn runs.
	if p, _, ok := strings.Cut(parentKey, "\x00"); ok {
		delete(s.allowed, key(p, id))
	}
	delete(s.children[parentKey], id)
	if len(s.children[parentKey]) == 0 {
		delete(s.children, parentKey)
	}
	s.mu.Unlock()
}

// waitFor returns a channel that receives how the next turn of the
// conversation ends. Register it before starting the turn.
func (s *Service) waitFor(k string) <-chan store.TurnStatus {
	ch := make(chan store.TurnStatus, 1)
	s.mu.Lock()
	s.waiters[k] = append(s.waiters[k], ch)
	s.mu.Unlock()
	return ch
}

// forgetWaiter removes a channel that waitFor returned and nobody will read.
func (s *Service) forgetWaiter(k string, ch <-chan store.TurnStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.waiters[k]
	if i := slices.IndexFunc(ws, func(w chan store.TurnStatus) bool { return (<-chan store.TurnStatus)(w) == ch }); i >= 0 {
		// A copy: notifyWaiters may hold the old slice.
		s.waiters[k] = slices.Delete(slices.Clone(ws), i, i+1)
	}
	if len(s.waiters[k]) == 0 {
		delete(s.waiters, k)
	}
}

func (s *Service) notifyWaiters(k string, status store.TurnStatus) {
	s.mu.Lock()
	ws := s.waiters[k]
	delete(s.waiters, k)
	s.mu.Unlock()
	for _, w := range ws {
		w <- status
	}
}

// childResult is what the parent's tool call returns for a finished sub-agent.
func (s *Service) childResult(ctx context.Context, a workspace.Actor, childID string, st store.TurnStatus) (string, error) {
	switch st {
	case store.TurnDone, store.TurnPaused:
		return s.finalText(ctx, childID), nil
	case store.TurnStopped:
		return "", errors.New("the sub-agent was stopped")
	default:
		return "", errors.New("the sub-agent failed")
	}
}

// finalText is the last thing the sub-agent said. A long run has more entries
// than one page, so it reads to the end.
func (s *Service) finalText(ctx context.Context, convID string) string {
	const page = 1000
	var after int64
	last := ""
	for {
		entries, err := s.d.Store.Entries(ctx, convID, after, page)
		if err != nil {
			return last
		}
		for _, e := range entries {
			if e.Role == session.RoleAssistant {
				last = e.Text
			}
		}
		if len(entries) < page {
			return last
		}
		after = entries[len(entries)-1].Seq
	}
}

// reportBack waits for a background sub-agent and tells the parent. A finished
// one reaches the parent as a message, which starts a turn when the parent is
// idle. One that was stopped or failed only raises the event.
func (s *Service) reportBack(a workspace.Actor, childID, title string, done <-chan store.TurnStatus) {
	var st store.TurnStatus
	select {
	case st = <-done:
	case <-s.closed:
		return
	}
	ctx := context.Background()
	parentKey := key(a.Principal, a.ChatID)
	s.forgetChild(parentKey, childID)
	s.bus.Publish(parentKey, session.Event{Kind: session.KindSubAgentDone, IDs: []string{childID}, Text: title})
	if !st.Finished() {
		return
	}
	msg := fmt.Sprintf("[Sub-agent finished: %s]\n%s", title, s.finalText(ctx, childID))
	if _, err := s.send(ctx, a.Principal, a.ChatID, session.Message{Text: msg}); err != nil {
		s.log.Warn("studio: could not tell the parent that a sub-agent finished", "chat", a.ChatID, "error", err)
	}
}

// stopChildren stops every running sub-agent of a conversation, wherever its
// turn runs: the store knows the children, and Stop reaches a turn on another
// frontend over the bus.
func (s *Service) stopChildren(p, conv string) {
	go func() {
		ctx := context.Background()
		kids, err := s.runningChildren(ctx, p, conv)
		if err != nil {
			s.log.Warn("studio: could not list the sub-agents to stop", "chat", conv, "error", err)
			return
		}
		for _, k := range kids {
			if err := s.Stop(ctx, p, k.ID); err != nil {
				s.log.Warn("studio: could not stop a sub-agent", "chat", k.ID, "error", err)
			}
		}
	}()
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return session.CapText(s, n) + "…"
}

// hasTool says whether the list has a tool of that name.
func hasTool(tools []session.Tool, name string) bool {
	return slices.ContainsFunc(tools, func(t session.Tool) bool { return t.Name == name })
}
