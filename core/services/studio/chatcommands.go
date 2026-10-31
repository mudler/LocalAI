package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// The slash commands of a chat that reach the server: a message sent with a
// skill or a saved agent (Command), and the two context commands, Compact and
// Clear. The navigation commands live in the interface only.

// Errors of the chat commands.
var (
	// ErrBadCommand means a message names a skill or an agent the chat cannot
	// use. The text after the sentinel is for the person.
	ErrBadCommand = errors.New("studio: the command cannot run")
	// ErrCommandBusy means a command message came while a turn runs. Its
	// instructions belong to the turn it starts, so it does not wait.
	ErrCommandBusy = errors.New("studio: a command runs when the model has answered; send it again then")
	// ErrNothingToCompact means the conversation is too short to compact.
	ErrNothingToCompact = errors.New("studio: the conversation is too short to compact")
	// ErrNothingToClear means the model sees no message to clear.
	ErrNothingToClear = errors.New("studio: the conversation has nothing to clear")
	// ErrCannotCompact means the chat's loop cannot compact on request.
	ErrCannotCompact = errors.New("studio: this chat cannot compact its context")
)

// Command is the slash command a message is sent with: at most one of Skill
// and Agent is set.
type Command struct {
	// Skill is the name of a skill of the chat.
	Skill string
	// Agent is the id or the name of one of the user's saved agents.
	Agent string
}

func (c Command) empty() bool { return c.Skill == "" && c.Agent == "" }

// maxListedNames bounds the names an error lists.
const maxListedNames = 40

// commandMessage checks cmd against the chat and returns what the message
// carries: the directive for the model and the invocation for the transcript.
func (s *Service) commandMessage(ctx context.Context, p string, rec session.ChatRecord, text string, cmd Command) (directive string, inv *session.Invocation, err error) {
	switch {
	case cmd.Skill != "" && cmd.Agent != "":
		return "", nil, fmt.Errorf("%w: a message takes a skill or an agent, not both", ErrBadCommand)
	case cmd.Skill != "":
		return s.skillDirective(ctx, p, rec, cmd.Skill, strings.TrimSpace(text) == "")
	default:
		if strings.TrimSpace(text) == "" {
			return "", nil, fmt.Errorf("%w: write the task for the agent after its name", ErrBadCommand)
		}
		return s.agentDirective(ctx, p, rec, cmd.Agent)
	}
}

// chatSkills resolves the skills the chat may use: valid, not shadowed, not
// switched off, and granted by the chat's agent when it has one. It is the set
// the turn's skill index lists.
func (s *Service) chatSkills(ctx context.Context, p string, rec session.ChatRecord) ([]skills.Skill, error) {
	if !s.d.Files.Available() {
		return nil, nil
	}
	sc := s.scopeOf(ctx, rec)
	skip := skillOff(rec.SkillsOff)
	ad := s.addonSkillsFor(ctx, p, rec.AddonsOff, sc)
	list, _, err := skills.Resolve(ctx, s.d.Files.Store(), s.d.Files.Actor(p), s.d.Files.SkillRoots(p, ad.roots()...), ad.options(skills.Options{
		Off:    func(sk skills.Skill) bool { return skip(sk) || (sc != nil && !sc.skills[sk.Name]) },
		Logger: s.log,
	}))
	if err != nil {
		return nil, err
	}
	var on []skills.Skill
	for _, sk := range list {
		if sk.Invalid == "" && sk.Shadowed == "" && !sk.Off {
			on = append(on, sk)
		}
	}
	return on, nil
}

// skillDirective finds the skill name in the chat's set and returns the
// instructions for the model: the skill's SKILL.md, read as the user, so the
// model gets the text the file tools would give it whether or not it has a
// tool to load skills.
func (s *Service) skillDirective(ctx context.Context, p string, rec session.ChatRecord, name string, noTask bool) (string, *session.Invocation, error) {
	list, err := s.chatSkills(ctx, p, rec)
	if err != nil {
		return "", nil, err
	}
	var found *skills.Skill
	names := make([]string, 0, len(list))
	for i := range list {
		names = append(names, list[i].Name)
		if list[i].Name == name {
			found = &list[i]
		}
	}
	if found == nil {
		slices.Sort(names)
		if len(names) == 0 {
			return "", nil, fmt.Errorf("%w: no skill %q in this chat; it has no skills", ErrBadCommand, name)
		}
		if len(names) > maxListedNames {
			names = append(names[:maxListedNames], "…")
		}
		return "", nil, fmt.Errorf("%w: no skill %q in this chat; its skills are: %s", ErrBadCommand, name, strings.Join(names, ", "))
	}
	if slices.Contains(found.Requires, skills.RequireExec) {
		if c := s.commandsFor(ctx, p, rec); c.State != CommandsAvailable {
			return "", nil, fmt.Errorf("%w: the skill %q needs to run commands, and this chat cannot", ErrBadCommand, name)
		}
	}
	body, err := s.d.Files.ReadSkillFile(ctx, p, found.Path)
	if err != nil {
		return "", nil, fmt.Errorf("studio: read the skill %q: %w", name, err)
	}
	return skillDirectiveText(found.Name, found.GuestPath, body, noTask), &session.Invocation{Kind: session.InvokeSkill, Name: found.Name}, nil
}

// skillDirectiveText is the note the model gets before a message sent with a
// skill.
func skillDirectiveText(name, guestPath, body string, noTask bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(System note: the user invoked the skill %q for this message. Follow its instructions, which come next", skills.Clean(name))
	if guestPath != "" {
		fmt.Fprintf(&b, " (the file is %s)", skills.Clean(guestPath))
	}
	if noTask {
		b.WriteString(". The message has no other text: start the skill now and ask the user for what it needs.")
	} else {
		b.WriteString(", to do what the message asks.")
	}
	fmt.Fprintf(&b, "\n<skill name=%q>\n%s\n</skill>)", skills.Clean(name), strings.TrimSpace(body))
	return b.String()
}

// agentDirective checks that the chat may call the saved agent ref and returns
// the note that asks the model to hand the task to it.
func (s *Service) agentDirective(ctx context.Context, p string, rec session.ChatRecord, ref string) (string, *session.Invocation, error) {
	if err := s.savedAgentsAllowed(ctx, rec); err != nil {
		switch {
		case errors.Is(err, ErrSavedAgentsNotAllowed):
			return "", nil, fmt.Errorf("%w: saved agents are off for this chat; switch them on in the Tools menu", ErrBadCommand)
		case errors.Is(err, ErrSavedAgentsOff), errors.Is(err, ErrSavedAgentsForbidden):
			return "", nil, fmt.Errorf("%w: %s", ErrBadCommand, err.Error())
		}
		return "", nil, err
	}
	ag, err := s.resolveAgent(ctx, p, ref)
	if err != nil {
		return "", nil, fmt.Errorf("%w: you have no saved agent %q", ErrBadCommand, strings.TrimSpace(ref))
	}
	directive := fmt.Sprintf("(System note: the user asked you to hand the task of this message to their saved agent %q. "+
		"Call spawn_agent with agent=%q and the task, then tell the user what the agent answered.)", skills.Clean(ag.Name), ag.ID)
	return directive, &session.Invocation{Kind: session.InvokeAgent, Name: ag.Name, ID: ag.ID}, nil
}

// SendCommand is SendWithFiles for a message sent with a slash command. With
// an empty command it is SendWithFiles. The command is checked before
// anything is saved; it does not wait for a running turn (ErrCommandBusy).
func (s *Service) SendCommand(ctx context.Context, user, conv, text string, up []session.Upload, cmd Command) (SendResult, error) {
	if cmd.empty() {
		return s.SendWithFiles(ctx, user, conv, text, up)
	}
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return SendResult{}, err
	}
	if rec.Kind == session.KindSubAgent {
		return SendResult{}, ErrSubAgentReadOnly
	}
	if info, err := s.d.Store.Turn(ctx, p, conv); err == nil && info.Running {
		return SendResult{}, ErrCommandBusy
	}
	directive, inv, err := s.commandMessage(ctx, p, rec, text, cmd)
	if err != nil {
		return SendResult{}, err
	}
	return s.sendWithFiles(ctx, user, conv, text, up, directive, inv)
}

// isThread reports whether the model sees at least one message of the person
// and one answer: what a context command needs to act on.
func isThread(entries []session.Entry) bool {
	var user, answer bool
	for _, e := range session.SinceCleared(entries) {
		user = user || e.Role == session.RoleUser
		answer = answer || e.Role == session.RoleAssistant
	}
	return user && answer
}

// contextCommand runs fn under the claim of a turn, so that no turn starts on
// any frontend while it changes what the model sees. It is ErrChatBusy while a
// turn runs. Messages that arrived meanwhile start a turn after it.
func (s *Service) contextCommand(ctx context.Context, p, conv string, fn func(rec session.ChatRecord, entries []session.Entry) error) error {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return err
	}
	if rec.Kind == session.KindSubAgent {
		return ErrSubAgentReadOnly
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	last := store.TurnDone
	if info, terr := s.d.Store.Turn(ctx, p, conv); terr == nil && info.Last != "" {
		last = info.Last
	}
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return ErrChatBusy
		}
		return err
	}
	// Read again under the claim: another frontend may have changed the chat.
	rec, err = s.d.Store.GetChat(ctx, p, conv)
	var entries []session.Entry
	if err == nil {
		entries, err = s.d.Store.Entries(ctx, conv, 0, 0)
	}
	if err == nil {
		err = fn(rec, entries)
	}
	// The chat reads as it did before: a paused turn stays paused.
	if rerr := s.releaseTurn(context.WithoutCancel(ctx), p, conv, runID, last); rerr != nil {
		s.log.Warn("studio: could not release the claim of a context command", "chat", conv, "error", rerr)
	}
	if left, perr := s.d.Store.Pending(ctx, p, conv); perr == nil && len(left) > 0 {
		if _, serr := s.startFromPending(context.WithoutCancel(ctx), p, conv); serr != nil {
			s.log.Warn("studio: could not start the waiting messages after a context command", "chat", conv, "error", serr)
		}
	}
	return err
}

// Compact shrinks the model's context of the chat now, with the summary the
// loop makes when the context fills up, and returns its size in tokens before
// and after. The transcript keeps every message and gets a marker; the next
// turn starts from the compacted state.
func (s *Service) Compact(ctx context.Context, user, conv string) (before, after int, err error) {
	p := principal(user)
	err = s.contextCommand(ctx, p, conv, func(_ session.ChatRecord, entries []session.Entry) error {
		if !isThread(entries) {
			return ErrNothingToCompact
		}
		chat, err := s.chat(ctx, p, conv)
		if err != nil {
			return err
		}
		// Nothing of a chat stays in memory past its work: another frontend
		// may run its next turn.
		defer func() {
			if cerr := chat.Close(); cerr != nil {
				s.log.Warn("studio: could not close the chat after a compaction", "chat", conv, "error", cerr)
			}
		}()
		before, after, err = chat.Compact(context.WithoutCancel(ctx))
		switch {
		case errors.Is(err, session.ErrNothingToCompact):
			return ErrNothingToCompact
		case errors.Is(err, session.ErrCannotCompact):
			return ErrCannotCompact
		}
		return err
	})
	return before, after, err
}

// Clear gives the chat a fresh context: a marker goes in the transcript and the
// model sees nothing before it from the next turn on. The files, the title and
// the switches of the chat stay.
func (s *Service) Clear(ctx context.Context, user, conv string) error {
	p := principal(user)
	return s.contextCommand(ctx, p, conv, func(rec session.ChatRecord, entries []session.Entry) error {
		if !slices.ContainsFunc(session.SinceCleared(entries), func(e session.Entry) bool { return session.ModelRole(e.Role) }) {
			return ErrNothingToClear
		}
		if _, err := s.d.Store.AppendEntry(ctx, conv, session.Entry{Turn: rec.Turns, Role: session.RoleCleared}); err != nil {
			return err
		}
		// No state: the next turn starts the loop from the transcript after
		// the marker, which is empty.
		rec.HarnessState, rec.Updated = nil, time.Now().UTC()
		if err := s.d.Store.UpdateChat(ctx, rec); err != nil {
			return err
		}
		s.bus.Publish(key(p, conv), session.Event{Kind: session.KindCleared, Turn: rec.Turns})
		return nil
	})
}
