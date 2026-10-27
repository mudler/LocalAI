package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
)

type child struct {
	id         string
	title      string
	background bool
}

// chatTools is the hook that gives every chat its host tools. A conversation
// gets the host tools plus spawn_agent; a sub-agent gets only the tools its
// parent chose and never spawn_agent.
func (s *Service) chatTools(ctx context.Context, a workspace.Actor) (session.TurnTools, error) {
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return session.TurnTools{}, err
	}
	if rec.Kind == session.KindSubAgent {
		s.mu.Lock()
		tools := s.allowed[key(a.Principal, a.ChatID)]
		s.mu.Unlock()
		return session.TurnTools{Tools: tools}, nil
	}
	tools, missing, err := s.scopedHostTools(ctx, a)
	if err != nil {
		s.log.Warn("studio: host tools unavailable", "chat", a.ChatID, "error", err)
	}
	sc := s.scopeOf(ctx, rec)
	// The file tools belong to the chat itself: a sub-agent never gets them,
	// because its tools come from hostTools, which does not include them.
	tools = append(tools, s.d.Files.Tools(a)...)
	if s.d.Media != nil {
		tools = append(tools, s.d.Media.Tools(s.d.Files, a)...)
	}
	prompt, note, key := s.skillTurn(ctx, a.Principal, rec.SkillsOff, sc)
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
	return session.TurnTools{Tools: append(tools, s.spawnTool(a)), Prompt: prompt, Note: note, RefreshKey: key}, nil
}

func (s *Service) spawnTool(a workspace.Actor) session.Tool {
	return session.Tool{
		Name: "spawn_agent",
		Description: "Start a sub-agent that works on a task and reports back. Use it for a self-contained piece of work. " +
			"By default you wait for its answer. With wait=false it runs in the background and its result arrives later as a message.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"task":{"type":"string","description":"What the sub-agent should do, with everything it needs to know."},` +
			`"tools":{"type":"array","items":{"type":"string"},"description":"Names of your tools the sub-agent may use. Omit to give it all of them; [] gives it none."},` +
			`"wait":{"type":"boolean","description":"true (default): wait for the answer. false: carry on now."}},` +
			`"required":["task"]}`),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return s.spawn(ctx, a, raw) },
	}
}

func (s *Service) spawn(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		Task  string    `json:"task"`
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

	subset, err := s.subset(ctx, a, args.Tools)
	if err != nil {
		return "", err
	}

	parentKey := key(a.Principal, a.ChatID)
	childID := newID()
	title := "Sub-agent: " + truncate(task, 60)
	if err := s.registerChild(parentKey, &child{id: childID, title: title, background: !wait}); err != nil {
		return "", err
	}
	now := parent.Updated
	rec := session.ChatRecord{ID: childID, Principal: a.Principal, Title: title, Created: now, Updated: now,
		Kind: session.KindSubAgent, ParentID: a.ChatID, Model: parent.Model}
	if err := s.d.Store.CreateChat(ctx, rec); err != nil {
		s.forgetChild(parentKey, childID)
		return "", err
	}
	childKey := key(a.Principal, childID)
	s.mu.Lock()
	s.allowed[childKey] = subset
	s.mu.Unlock()
	done := s.waitFor(childKey)
	if _, err := s.send(context.WithoutCancel(ctx), a.Principal, childID, task, nil); err != nil {
		s.forgetChild(parentKey, childID)
		return "", err
	}

	if !wait {
		go s.reportBack(a, childID, title, done)
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

// subset picks the tools a sub-agent gets from the parent's host tools. Nil
// asks for all of them but the named-only ones; a name the parent does not have is an error.
func (s *Service) subset(ctx context.Context, a workspace.Actor, names *[]string) ([]session.Tool, error) {
	have, err := s.hostTools(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("could not list your tools: %w", err)
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
// the limit.
func (s *Service) registerChild(parentKey string, c *child) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.background {
		n := 0
		for _, k := range s.children[parentKey] {
			if k.background {
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
	case store.TurnDone:
		return s.finalText(ctx, childID), nil
	case store.TurnStopped:
		return "", errors.New("the sub-agent was stopped")
	default:
		return "", errors.New("the sub-agent failed")
	}
}

// finalText is the last thing the sub-agent said.
func (s *Service) finalText(ctx context.Context, convID string) string {
	entries, err := s.d.Store.Entries(ctx, convID, 0, 1000)
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == session.RoleAssistant {
			return entries[i].Text
		}
	}
	return ""
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
	if st != store.TurnDone {
		return
	}
	msg := fmt.Sprintf("[Sub-agent finished: %s]\n%s", title, s.finalText(ctx, childID))
	if _, err := s.send(ctx, a.Principal, a.ChatID, msg, nil); err != nil {
		s.log.Warn("studio: could not tell the parent that a sub-agent finished", "chat", a.ChatID, "error", err)
	}
}

// stopChildren stops every running sub-agent of a conversation.
func (s *Service) stopChildren(p, conv string) {
	s.mu.Lock()
	var ids []string
	for id := range s.children[key(p, conv)] {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		go func(id string) {
			if err := s.Stop(context.Background(), p, id); err != nil {
				s.log.Warn("studio: could not stop a sub-agent", "chat", id, "error", err)
			}
		}(id)
	}
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
