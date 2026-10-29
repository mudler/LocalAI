package studiotest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ToolStep is one action of a scripted turn. Set one field.
type ToolStep struct {
	Say   string          // emit a text event
	Think string          // emit a reasoning event
	Tool  string          // call the host tool with this name ...
	Args  string          // ... with these JSON arguments
	Block <-chan struct{} // wait until the channel closes or the turn ends
	Err   error           // end the turn with this error
}

// ToolResult is what one scripted tool call returned.
type ToolResult struct {
	Chat string
	Tool string
	Out  string
	Err  error
}

// ToolHarness is a session.Harness whose turns are decided by Script, per chat
// and per message. It calls the host tools the chat was started with, so a test
// can drive tools such as spawn_agent and see what they returned.
type ToolHarness struct {
	// Script returns the steps of the turn for this chat and message.
	Script func(a workspace.Actor, text string) []ToolStep

	mu      sync.Mutex
	results []ToolResult
	sent    map[string][]string
	tools   map[string][]string
	prompts map[string]string
}

// Results returns every tool call so far.
func (h *ToolHarness) Results() []ToolResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ToolResult(nil), h.results...)
}

// ResultsFor returns the tool calls of one tool, in order.
func (h *ToolHarness) ResultsFor(tool string) []ToolResult {
	var out []ToolResult
	for _, r := range h.Results() {
		if r.Tool == tool {
			out = append(out, r)
		}
	}
	return out
}

// Sent returns the messages a chat's turns were given.
func (h *ToolHarness) Sent(chat string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.sent[chat]...)
}

// ToolNames returns the names of the tools the chat's session was last started with.
func (h *ToolHarness) ToolNames(chat string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.tools[chat]...)
}

// TurnPrompt returns the block the chat's session was last started with, after
// the system prompt: what the product adds for a turn, such as the skill index.
func (h *ToolHarness) TurnPrompt(chat string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.prompts[chat]
}

// Start implements session.Harness.
func (h *ToolHarness) Start(_ context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	h.mu.Lock()
	if h.tools == nil {
		h.tools, h.sent = map[string][]string{}, map[string][]string{}
	}
	names := make([]string, len(env.Tools))
	for i, t := range env.Tools {
		names[i] = t.Name
	}
	h.tools[env.Actor.ChatID] = names
	if h.prompts == nil {
		h.prompts = map[string]string{}
	}
	h.prompts[env.Actor.ChatID] = env.TurnPrompt
	h.mu.Unlock()
	return &toolSession{h: h, env: env}, nil
}

type toolSession struct {
	h   *ToolHarness
	env session.HarnessEnv

	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *toolSession) Send(ctx context.Context, text string) error {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()
	chat := s.env.Actor.ChatID
	s.h.mu.Lock()
	s.h.sent[chat] = append(s.h.sent[chat], text)
	s.h.mu.Unlock()
	for _, step := range s.h.Script(s.env.Actor, text) {
		switch {
		case step.Say != "":
			s.env.Emit(session.Event{Kind: session.KindText, Text: step.Say})
		case step.Think != "":
			s.env.Emit(session.Event{Kind: session.KindReasoning, Text: step.Think})
		case step.Tool != "":
			s.env.Emit(session.Event{Kind: session.KindToolStart, Tool: &session.ToolEvent{Name: step.Tool, Summary: session.Summarize(json.RawMessage(step.Args))}})
			out, err := s.call(ctx, step.Tool, step.Args)
			text := out
			if err != nil {
				text = err.Error()
			}
			s.env.Emit(session.Event{Kind: session.KindToolEnd, Tool: &session.ToolEvent{Name: step.Tool,
				Summary: session.Summarize(json.RawMessage(step.Args)), OK: err == nil, Output: session.CapText(text, 2048)}})
			s.h.mu.Lock()
			s.h.results = append(s.h.results, ToolResult{Chat: chat, Tool: step.Tool, Out: out, Err: err})
			s.h.mu.Unlock()
		case step.Block != nil:
			select {
			case <-step.Block:
			case <-ctx.Done():
				return ctx.Err()
			}
		case step.Err != nil:
			return step.Err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func (s *toolSession) call(ctx context.Context, name, args string) (string, error) {
	for _, tool := range s.env.Tools {
		if tool.Name == name {
			return tool.Run(ctx, json.RawMessage(args))
		}
	}
	return "", fmt.Errorf("no tool named %q", name)
}

func (s *toolSession) Inject(string) error { return session.ErrNoTurn }

func (s *toolSession) Interrupt() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *toolSession) Export() ([]byte, error) { return []byte(`{}`), nil }
func (s *toolSession) Close() error            { return s.Interrupt() }
