package studio_test

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// armingLLM is a model that, on the first request that offers tools, calls
// wait_for with the given arguments, and then answers. It records the tools it
// was offered.
type armingLLM struct {
	mu    sync.Mutex
	args  string
	tools [][]string
	calls int
}

func (l *armingLLM) Ask(_ context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage(cogito.AssistantMessageRole, "ok"), nil
}

func (l *armingLLM) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var names []string
	for _, t := range req.Tools {
		if t.Function != nil {
			names = append(names, t.Function.Name)
		}
	}
	l.tools = append(l.tools, names)
	msg := openai.ChatCompletionMessage{Role: "assistant", Content: "armed"}
	if l.calls == 0 && len(names) > 0 {
		l.calls++
		msg = openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call-1", Type: "function",
			Function: openai.FunctionCall{Name: "wait_for", Arguments: l.args}}}}
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: msg}}}},
		cogito.LLMUsage{PromptTokens: 10, TotalTokens: 11}, nil
}

// The real tool set of nib, not a stand-in harness: a host tool named like a
// built-in of nib once switched the built-in on, and nib answered instead of
// the host. This arms a script wait through nib's own wait_for and checks that
// the call reaches the handler of the chat service, with the host's bash
// offered next to it and never run by nib.
var _ = Describe("wait_for on a script through nib's own tools", func() {
	It("reaches the host's handler with the script and the status condition, and nib runs nothing", func() {
		llm := &armingLLM{args: `{"script":"test -f /tmp/ready","until":"exit_zero","interval_seconds":60,"timeout_seconds":600,` +
			`"then":"Tell the person the file is there.","reason":"the file"}`}
		status := 1
		fx := &scriptHostExec{}
		fx.set(exits("", &status))
		harness := nibharness.New(nibharness.Config{LLM: func(workspace.Actor) (nibharness.Model, error) {
			return nibharness.Model{LLM: llm, Name: "test-model"}, nil
		}})
		r := newLoopRig(say, studio.LoopOptions{}, func(d *studio.Deps) {
			d.Harness = harness
			d.HostExec = fx
			d.HostExecMode = func() string { return "on" }
		})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)

		llm.mu.Lock()
		offered := llm.tools[0]
		llm.mu.Unlock()
		count := map[string]int{}
		for _, n := range offered {
			count[n]++
		}
		Expect(count["wait_for"]).To(Equal(1), "offered: %v", offered)
		Expect(count["bash"]).To(Equal(1), "offered: %v", offered)

		list, err := r.host.List(context.Background(), alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1), "nib did not hand the wait to the chat service")
		w := list[0].Watch
		Expect(w.Script).To(Equal("test -f /tmp/ready"))
		Expect(w.Mode).To(Equal(loops.UntilExitZero))
		Expect(list[0].Interval).To(Equal(time.Minute))
		Expect(fx.commands()).To(BeEmpty(), "bash ran when a wait was armed")

		// The check goes through the host's read-only call, not through nib.
		r.pass()
		n, scripts := fx.calls()
		Expect(n).To(Equal(1))
		Expect(scripts).To(Equal([]string{"test -f /tmp/ready"}))
	})
})
