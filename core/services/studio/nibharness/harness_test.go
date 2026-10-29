package nibharness_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func TestNibharness(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio nib harness")
}

func start(llm *recordingLLM, env session.HarnessEnv, tweak ...func(*nibharness.Model)) session.HarnessSession {
	ctxTokens := 0
	if llm.contextTokens > 0 {
		ctxTokens = llm.contextTokens
	}
	h := nibharness.New(nibharness.Config{
		LLM: func(a workspace.Actor) (nibharness.Model, error) {
			defer GinkgoRecover()
			Expect(a.Principal).To(Equal("alice"), "LLM asked for actor %+v", a)
			m := nibharness.Model{LLM: llm, Name: "host-model", ContextTokens: ctxTokens}
			for _, f := range tweak {
				f(&m)
			}
			return m, nil
		},
	})
	env.Dir = GinkgoT().TempDir()
	env.SystemPrompt = "You are a test."
	env.Actor = workspace.Actor{Principal: "alice", ChatID: "c1"}
	if env.Emit == nil {
		env.Emit = func(session.Event) {}
	}
	hs, err := h.Start(context.Background(), env)
	Expect(err).ToNot(HaveOccurred(), "Start")
	DeferCleanup(func() { _ = hs.Close() })
	return hs
}

func send(hs session.HarnessSession, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	Expect(hs.Send(ctx, text)).To(Succeed(), "Send")
}

// callingLLM asks for one call of a named tool on its first request, then answers.
type callingLLM struct {
	recordingLLM
	call string
}

func (c *callingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	c.record("CreateChatCompletion", req.Messages, req.Tools)
	c.mu.Lock()
	first := len(c.tools) == 1
	c.mu.Unlock()
	msg := openai.ChatCompletionMessage{Role: "assistant", Content: "all done"}
	if first {
		msg = openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call-1", Type: "function",
			Function: openai.FunctionCall{Name: c.call, Arguments: "{}"}}}}
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: msg}}}},
		cogito.LLMUsage{PromptTokens: 10, TotalTokens: 11}, nil
}

// hangingLLM blocks every request until its context ends, and tells when it did.
type hangingLLM struct {
	recordingLLM
	started   chan struct{}
	cancelled chan struct{}
}

func (h *hangingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case h.cancelled <- struct{}{}:
	default:
	}
	return cogito.LLMReply{}, cogito.LLMUsage{}, ctx.Err()
}

var _ = Describe("the nib harness", func() {
	It("uses the host model and delivers the batch", func() {
		llm := &recordingLLM{}
		var mu sync.Mutex
		asked, settled := 0, []error{}
		hostTool := session.Tool{
			Name: "host_tool", Description: "a host tool", Schema: json.RawMessage(`{"type":"object"}`),
			Run: func(context.Context, json.RawMessage) (string, error) { return "done", nil },
		}
		hs := start(llm, session.HarnessEnv{
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
		send(hs, "hello")

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
		Expect(sawHello).To(BeTrue(), "the host model must see the user text")
		Expect(sawTool).To(BeTrue(), "the host model must see the host tool")
		Expect(batch).To(HaveLen(1), "want both extras in one message")
		Expect(strings.Index(batch[0], "extra one")).To(BeNumerically("<=", strings.Index(batch[0], "extra two")), "extras must be in order")
		mu.Lock()
		defer mu.Unlock()
		Expect(settled).To(HaveLen(1), "want one successful settle")
		Expect(settled[0]).ToNot(HaveOccurred())
	})

	// A chat with no host tools (a sub-agent that only thinks) must not hand the
	// model nib's own shell, file or web tools.
	It("without tools offers no builtin tools", func() {
		llm := &recordingLLM{}
		hs := start(llm, session.HarnessEnv{})
		send(hs, "hello")
		Expect(llm.tools).ToNot(BeEmpty(), "the model received no request")
		GinkgoWriter.Printf("requests: %v; tools offered to the first: %v\n", llm.via, llm.tools[0])
		banned := []string{"bash", "shell", "file", "edit", "write", "fetch", "search", "browser", "computer", "glob", "grep"}
		for _, name := range llm.tools[0] {
			for _, b := range banned {
				Expect(strings.ToLower(name)).ToNot(ContainSubstring(b), "tool %q is offered to a chat that has no tools; all offered: %v", name, llm.tools[0])
			}
		}
	})

	// The caller cancels its context as soon as Send returns. The watcher that
	// turns a cancelled context into an interrupt must not outlive the call, or
	// it interrupts the next turn of the same session.
	It("a context cancelled after Send returns does not interrupt the next turn", func() {
		hs := start(&recordingLLM{}, session.HarnessEnv{})
		for i := 0; i < 300; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			Expect(hs.Send(ctx, "first")).To(Succeed(), "send %d", i)
			cancel()
			send(hs, "second")
		}
	})

	// nib compacts when a prompt fills most of the model's context. The context size
	// is the model's, not nib's default, so a small model compacts early; the host
	// hears about it.
	It("compaction is reported and uses the model context size", func() {
		llm := &recordingLLM{contextTokens: 2000, promptTokens: 1900}
		var mu sync.Mutex
		var compacted []session.Event
		hs := start(llm, session.HarnessEnv{
			Emit: func(e session.Event) {
				if e.Kind == session.KindCompacted {
					mu.Lock()
					compacted = append(compacted, e)
					mu.Unlock()
				}
			},
		})
		for i := 0; i < 8; i++ {
			send(hs, "message number "+string(rune('a'+i))+" with some words so there is history to compact")
		}
		mu.Lock()
		defer mu.Unlock()
		Expect(compacted).ToNot(BeEmpty(), "no compaction was reported after 8 turns with a 1900-token prompt in a 2000-token context; requests=%d", len(llm.messages))
		Expect(compacted[0].Before).ToNot(BeZero(), "compaction event = %+v", compacted[0])
		Expect(compacted[0].After).To(BeNumerically("<", compacted[0].Before), "compaction event = %+v, want a smaller context after", compacted[0])
	})

	// The control for the test above: with the default context window the same
	// conversation does not compact, so the model's context size is what decides.
	It("does not compact in a large context", func() {
		llm := &recordingLLM{contextTokens: 200000, promptTokens: 1900}
		var mu sync.Mutex
		compacted := 0
		hs := start(llm, session.HarnessEnv{
			Emit: func(e session.Event) {
				if e.Kind == session.KindCompacted {
					mu.Lock()
					compacted++
					mu.Unlock()
				}
			},
		})
		for i := 0; i < 8; i++ {
			send(hs, "message number "+string(rune('a'+i))+" with some words so there is history to compact")
		}
		mu.Lock()
		defer mu.Unlock()
		Expect(compacted).To(BeZero(), "a 1900-token prompt in a 200000-token context compacted")
	})

	// nib adds text about itself to the system prompt: a skill that documents nib,
	// the load_skill tool that reads skills, and how to add MCP servers from nib's
	// command line. None of it is true for a chat of LocalAI, where the model has no
	// such skill, tool or command.
	It("has nothing of nib itself in the prompt", func() {
		llm := &recordingLLM{}
		hs := start(llm, session.HarnessEnv{})
		send(hs, "hello")
		var system string
		for _, req := range llm.messages {
			for _, m := range req {
				if m.Role == "system" {
					system += m.Content + "\n"
				}
			}
		}
		Expect(system).To(ContainSubstring("You are a test."), "the host prompt is missing")
		for _, leak := range []string{"about-nib", "load_skill", "mcp add", "AI terminal assistant", "self_read"} {
			Expect(system).ToNot(ContainSubstring(leak), "the system prompt holds %q", leak)
		}
	})

	// A host tool named like a built-in tool of nib must not switch the built-in
	// on: the model would be offered two tools of one name with different schemas,
	// and the built-in would answer the call instead of the host tool. The model
	// still sees, and calls, the host tool by its own name.
	It("offers a host tool named like a nib tool once and runs it on the host", func() {
		llm := &callingLLM{call: "spawn_agent"}
		var mu sync.Mutex
		ran := map[string]int{}
		mk := func(name string) session.Tool {
			return session.Tool{Name: name, Description: "host " + name, Schema: json.RawMessage(`{"type":"object"}`),
				Run: func(context.Context, json.RawMessage) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					ran[name]++
					return "ok", nil
				}}
		}
		var events []session.Event
		hs := start(&llm.recordingLLM, session.HarnessEnv{
			Tools: []session.Tool{mk("spawn_agent"), mk("transcribe_audio"), mk("host_tool")},
			Emit: func(e session.Event) {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, e)
			},
		}, func(m *nibharness.Model) { m.LLM = llm })
		send(hs, "hello")

		Expect(llm.tools).ToNot(BeEmpty(), "the model got no request")
		seen := map[string]int{}
		for _, n := range llm.tools[0] {
			seen[n]++
		}
		for n, c := range seen {
			Expect(c).To(Equal(1), "tool %q is offered %d times: %v", n, c, llm.tools[0])
		}
		for _, name := range []string{"spawn_agent", "transcribe_audio", "host_tool"} {
			Expect(seen[name]).To(Equal(1), "the model is not offered %q exactly once: %v", name, llm.tools[0])
		}
		for _, native := range []string{"check_agent", "get_agent_result", "send_agent_message", "studio_spawn_agent"} {
			Expect(seen[native]).To(BeZero(), "tool %q is offered: %v", native, llm.tools[0])
		}
		mu.Lock()
		defer mu.Unlock()
		Expect(ran["spawn_agent"]).To(Equal(1), "the host's spawn_agent must run once")
		named := false
		for _, e := range events {
			if e.Tool != nil && e.Tool.Name == "spawn_agent" {
				named = true
			}
			if e.Tool != nil {
				Expect(e.Tool.Name).ToNot(HavePrefix("studio_"), "an event carries the alias %q", e.Tool.Name)
			}
		}
		Expect(named).To(BeTrue(), "no tool event names spawn_agent: %+v", events)
		// The history the model sees on the second request names the tool as the host does.
		for _, m := range llm.messages[len(llm.messages)-1] {
			for _, tc := range m.ToolCalls {
				Expect(tc.Function.Name).To(Equal("spawn_agent"), "the history names the call %q", tc.Function.Name)
			}
		}
	})

	// Stopping a turn, and deleting a running chat, must cancel the model request
	// that is in flight, not leave it holding a slot of the backend.
	It("interrupt cancels the model request in flight", func() {
		llm := &hangingLLM{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1)}
		h := nibharness.New(nibharness.Config{
			LLM: func(workspace.Actor) (nibharness.Model, error) { return nibharness.Model{LLM: llm, Name: "m"}, nil },
		})
		hs, err := h.Start(context.Background(), session.HarnessEnv{
			Dir: GinkgoT().TempDir(), SystemPrompt: "x", Actor: workspace.Actor{Principal: "alice", ChatID: "c1"}, Emit: func(session.Event) {},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = hs.Close() })
		sent := make(chan error, 1)
		go func() { sent <- hs.Send(context.Background(), "hello") }()
		Eventually(llm.started, 20*time.Second).Should(Receive(), "the model got no request")
		Expect(hs.Interrupt()).To(Succeed())
		Eventually(llm.cancelled, 5*time.Second).Should(Receive(), "Interrupt left the model request open")
		Eventually(sent, 10*time.Second).Should(Receive(), "Send did not return after Interrupt")
	})
})
