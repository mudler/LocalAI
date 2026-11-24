package nibharness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// thinkingLLM reasons, calls the host tool once per turn, then answers with
// reasoning, and records the whole request (messages and tools) as JSON.
type thinkingLLM struct {
	mu       sync.Mutex
	requests []openai.ChatCompletionRequest
}

func (l *thinkingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage(cogito.AssistantMessageRole, "ok"), nil
}

func (l *thinkingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	l.mu.Lock()
	l.requests = append(l.requests, req)
	n := len(l.requests)
	l.mu.Unlock()
	last := req.Messages[len(req.Messages)-1]
	msg := openai.ChatCompletionMessage{Role: "assistant", ReasoningContent: fmt.Sprintf("thinking about request %d", n)}
	if last.Role == "user" {
		msg.ToolCalls = []openai.ToolCall{{ID: fmt.Sprintf("call-%d", n), Type: "function",
			Function: openai.FunctionCall{Name: "host_tool", Arguments: `{"q":"x"}`}}}
	} else {
		msg.Content = fmt.Sprintf("answer %d", n)
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: msg}}}},
		cogito.LLMUsage{PromptTokens: 100, TotalTokens: 101}, nil
}

// rendered is the part of a request a server's prompt cache sees: the tools
// and the messages, in order.
func rendered(req openai.ChatCompletionRequest) []string {
	var out []string
	for _, t := range req.Tools {
		b, _ := json.Marshal(t)
		out = append(out, "tool "+string(b))
	}
	for _, m := range req.Messages {
		b, _ := json.Marshal(m)
		out = append(out, string(b))
	}
	return out
}

var _ = Describe("the prompt across a turn boundary", func() {
	// A chat closes its harness after each turn and the next turn starts it
	// again from the exported state, as the studio does. The first request of
	// the next turn must start with the last request of the turn before, so
	// that the server reuses its prompt cache and evaluates only what is new.
	It("keeps the last request of a turn as the prefix of the next turn's first request", func() {
		llm := &thinkingLLM{}
		h := nibharness.New(nibharness.Config{LLM: func(workspace.Actor) (nibharness.Model, error) {
			return nibharness.Model{LLM: llm, Name: "host-model", ContextTokens: 32768}, nil
		}})
		dir := GinkgoT().TempDir()
		env := session.HarnessEnv{
			Dir: dir, SystemPrompt: "You are a test.", TurnPrompt: "Turn prompt.",
			Actor: workspace.Actor{Principal: "alice", ChatID: "c1"},
			Emit:  func(session.Event) {},
			Tools: []session.Tool{{
				Name: "host_tool", Description: "a host tool", Schema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
				Run: func(context.Context, json.RawMessage) (string, error) { return "tool output", nil },
			}},
		}
		turn := func(resume []byte, text string) []byte {
			env.Resume = resume
			hs, err := h.Start(context.Background(), env)
			Expect(err).NotTo(HaveOccurred())
			send(hs, text)
			state, err := hs.Export()
			Expect(err).NotTo(HaveOccurred())
			Expect(hs.Close()).To(Succeed())
			return state
		}
		state := turn(nil, "first question")
		llm.mu.Lock()
		lastOfTurn1 := len(llm.requests) - 1
		llm.mu.Unlock()
		turn(state, "second question")

		llm.mu.Lock()
		defer llm.mu.Unlock()
		Expect(len(llm.requests)).To(BeNumerically(">", lastOfTurn1+1))
		before := rendered(llm.requests[lastOfTurn1])
		after := rendered(llm.requests[lastOfTurn1+1])
		for i := range before {
			Expect(i).To(BeNumerically("<", len(after)), "the next turn's request is shorter")
			Expect(after[i]).To(Equal(before[i]), "the prompt differs at part %d of %d", i, len(before))
		}
	})
})
