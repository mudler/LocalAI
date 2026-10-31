package nibharness

import (
	"context"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
)

// plainFake is a client that does not stream.
type plainFake struct{ cogito.LLM }

// streamingFake sees the requests it gets and streams one tool call.
type streamingFake struct {
	cogito.LLM
	got []openai.ChatCompletionRequest
}

func (f *streamingFake) CreateChatCompletionStream(_ context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	f.got = append(f.got, req)
	ch := make(chan cogito.StreamEvent, 2)
	ch <- cogito.StreamEvent{Type: cogito.StreamEventToolCall, ToolName: "spawn_agent", ToolCallIndex: 0}
	ch <- cogito.StreamEvent{Type: cogito.StreamEventDone}
	close(ch)
	return ch, nil
}

var _ = Describe("tool aliases", func() {
	It("the streaming form renames both ways", func() {
		fake := &streamingFake{}
		llm := withAliases(fake, []string{"spawn_agent", "plain"})
		sl, ok := llm.(cogito.StreamingLLM)
		Expect(ok).To(BeTrue(), "a client that streams must still stream once wrapped")
		req := openai.ChatCompletionRequest{Tools: []openai.Tool{
			{Type: "function", Function: &openai.FunctionDefinition{Name: "studio_spawn_agent"}},
			{Type: "function", Function: &openai.FunctionDefinition{Name: "plain"}},
		}}
		ch, err := sl.CreateChatCompletionStream(context.Background(), req)
		Expect(err).ToNot(HaveOccurred())
		var names []string
		for ev := range ch {
			if ev.ToolName != "" {
				names = append(names, ev.ToolName)
			}
		}
		Expect(names).To(Equal([]string{"studio_spawn_agent"}), "the loop must see the call as the alias")
		Expect(fake.got[0].Tools[0].Function.Name).To(Equal("spawn_agent"), "the model must be offered the host name")
		Expect(req.Tools[0].Function.Name).To(Equal("studio_spawn_agent"), "the caller's request was changed")
	})

	It("a client that does not stream does not start to look like one", func() {
		llm := withAliases(&plainFake{}, []string{"spawn_agent"})
		_, ok := llm.(cogito.StreamingLLM)
		Expect(ok).To(BeFalse(), "the wrapper claims to stream")
	})

	It("nothing is wrapped without an alias", func() {
		inner := &plainFake{}
		Expect(withAliases(inner, []string{"plain", "other"})).To(BeIdenticalTo(cogito.LLM(inner)), "a model with no colliding tool must be used as it is")
	})
})
