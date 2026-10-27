package nibharness_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// recordingLLM answers plainly and records every request: its messages and the
// names of the tools it was offered.
type recordingLLM struct {
	mu       sync.Mutex
	messages [][]openai.ChatCompletionMessage
	// contextTokens is the context size the harness is given for the model, and
	// promptTokens is what every reply claims the prompt used.
	contextTokens int
	promptTokens  int
	tools         [][]string
	via           []string
}

func (r *recordingLLM) record(via string, msgs []openai.ChatCompletionMessage, tools []openai.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.via = append(r.via, via)
	r.messages = append(r.messages, append([]openai.ChatCompletionMessage(nil), msgs...))
	var names []string
	for _, t := range tools {
		if t.Function != nil {
			names = append(names, t.Function.Name)
		}
	}
	r.tools = append(r.tools, names)
}

func (r *recordingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	r.record("Ask", f.Messages, nil)
	return f.AddMessage(cogito.AssistantMessageRole, "ok"), nil
}

func (r *recordingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	r.record("CreateChatCompletion", req.Messages, req.Tools)
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "ok"}}},
	}}, cogito.LLMUsage{PromptTokens: r.promptTokens, TotalTokens: r.promptTokens + 1}, nil
}

func start(t *testing.T, llm *recordingLLM, env session.HarnessEnv) session.HarnessSession {
	t.Helper()
	ctxTokens := 0
	if llm.contextTokens > 0 {
		ctxTokens = llm.contextTokens
	}
	h := nibharness.New(nibharness.Config{
		LLM: func(a workspace.Actor) (nibharness.Model, error) {
			if a.Principal != "alice" {
				t.Errorf("LLM asked for actor %+v", a)
			}
			return nibharness.Model{LLM: llm, Name: "host-model", ContextTokens: ctxTokens}, nil
		},
	})
	env.Dir = t.TempDir()
	env.SystemPrompt = "You are a test."
	env.Actor = workspace.Actor{Principal: "alice", ChatID: "c1"}
	if env.Emit == nil {
		env.Emit = func(session.Event) {}
	}
	hs, err := h.Start(context.Background(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = hs.Close() })
	return hs
}

func send(t *testing.T, hs session.HarnessSession, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := hs.Send(ctx, text); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestHarnessUsesTheHostModelAndDeliversTheBatch(t *testing.T) {
	llm := &recordingLLM{}
	var mu sync.Mutex
	asked, settled := 0, []error{}
	hostTool := session.Tool{
		Name: "host_tool", Description: "a host tool", Schema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "done", nil },
	}
	hs := start(t, llm, session.HarnessEnv{
		Tools: []session.Tool{hostTool},
		Inputs: func() ([]string, func(error)) {
			mu.Lock()
			defer mu.Unlock()
			asked++
			if asked > 1 {
				return nil, nil
			}
			return []string{"extra one", "extra two"}, func(err error) {
				mu.Lock()
				defer mu.Unlock()
				settled = append(settled, err)
			}
		},
	})
	send(t, hs, "hello")

	var batch []string
	sawHello, sawTool := false, false
	for i, req := range llm.messages {
		for _, m := range req {
			if m.Role == "user" && strings.Contains(m.Content, "hello") {
				sawHello = true
			}
			if m.Role == "user" && strings.Contains(m.Content, "extra one") {
				batch = append(batch, m.Content)
			}
		}
		for _, n := range llm.tools[i] {
			if n == "host_tool" {
				sawTool = true
			}
		}
	}
	if !sawHello || !sawTool {
		t.Fatalf("the host model must see the user text and the host tool: hello=%v tool=%v", sawHello, sawTool)
	}
	if len(batch) != 1 || strings.Index(batch[0], "extra one") > strings.Index(batch[0], "extra two") {
		t.Fatalf("want both extras in one message, in order; got %q", batch)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(settled) != 1 || settled[0] != nil {
		t.Fatalf("settle = %v, want one successful settle", settled)
	}
}

// A chat with no host tools (a sub-agent that only thinks) must not hand the
// model nib's own shell, file or web tools.
func TestHarnessWithoutToolsOffersNoBuiltinTools(t *testing.T) {
	llm := &recordingLLM{}
	hs := start(t, llm, session.HarnessEnv{})
	send(t, hs, "hello")
	if len(llm.tools) == 0 {
		t.Fatal("the model received no request")
	}
	t.Logf("requests: %v; tools offered to the first: %v", llm.via, llm.tools[0]) //nolint:forbidigo // plain testing.T test; this package has no Ginkgo suite
	banned := []string{"bash", "shell", "file", "edit", "write", "fetch", "search", "browser", "computer", "glob", "grep"}
	for _, name := range llm.tools[0] {
		for _, b := range banned {
			if strings.Contains(strings.ToLower(name), b) {
				t.Errorf("tool %q is offered to a chat that has no tools; all offered: %v", name, llm.tools[0])
			}
		}
	}
}

// nib compacts when a prompt fills most of the model's context. The context size
// is the model's, not nib's default, so a small model compacts early; the host
// hears about it.
func TestCompactionIsReportedAndUsesTheModelContextSize(t *testing.T) {
	llm := &recordingLLM{contextTokens: 2000, promptTokens: 1900}
	var mu sync.Mutex
	var compacted []session.Event
	hs := start(t, llm, session.HarnessEnv{
		Emit: func(e session.Event) {
			if e.Kind == session.KindCompacted {
				mu.Lock()
				compacted = append(compacted, e)
				mu.Unlock()
			}
		},
	})
	for i := 0; i < 8; i++ {
		send(t, hs, "message number "+string(rune('a'+i))+" with some words so there is history to compact")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(compacted) == 0 {
		t.Fatalf("no compaction was reported after 8 turns with a 1900-token prompt in a 2000-token context; requests=%d", len(llm.messages))
	}
	if compacted[0].Before == 0 || compacted[0].After >= compacted[0].Before {
		t.Fatalf("compaction event = %+v, want a smaller context after", compacted[0])
	}
}

// The control for the test above: with the default context window the same
// conversation does not compact, so the model's context size is what decides.
func TestNoCompactionInALargeContext(t *testing.T) {
	llm := &recordingLLM{contextTokens: 200000, promptTokens: 1900}
	var mu sync.Mutex
	compacted := 0
	hs := start(t, llm, session.HarnessEnv{
		Emit: func(e session.Event) {
			if e.Kind == session.KindCompacted {
				mu.Lock()
				compacted++
				mu.Unlock()
			}
		},
	})
	for i := 0; i < 8; i++ {
		send(t, hs, "message number "+string(rune('a'+i))+" with some words so there is history to compact")
	}
	mu.Lock()
	defer mu.Unlock()
	if compacted != 0 {
		t.Fatalf("a 1900-token prompt in a 200000-token context compacted %d times", compacted)
	}
}
