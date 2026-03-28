package agents

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockLLM implements cogito.LLM and returns a fixed response.
type mockLLM struct {
	response  string
	callCount atomic.Int32
}

func (m *mockLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	m.callCount.Add(1)
	return f.AddMessage(cogito.AssistantMessageRole, m.response), nil
}

func (m *mockLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	m.callCount.Add(1)
	msg := openai.ChatCompletionMessage{
		Role:    "assistant",
		Content: m.response,
	}
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{Message: msg}},
		},
	}, cogito.LLMUsage{}, nil
}

// statusCollector records status callbacks in a thread-safe way.
type statusCollector struct {
	mu       sync.Mutex
	statuses []string
}

func (sc *statusCollector) onStatus(s string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.statuses = append(sc.statuses, s)
}

func (sc *statusCollector) get() []string {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	cp := make([]string, len(sc.statuses))
	copy(cp, sc.statuses)
	return cp
}

var _ = DescribeTable("stripThinkingTags",
	func(input, want string) {
		Expect(stripThinkingTags(input)).To(Equal(want))
	},
	Entry("empty string", "", ""),
	Entry("no tags", "Hello, world!", "Hello, world!"),
	Entry("single tag pair", "before<thinking>secret thoughts</thinking>after", "beforeafter"),
	Entry("multiple tag pairs", "a<thinking>one</thinking>b<thinking>two</thinking>c", "abc"),
	Entry("nested tags", "<thinking>outer<thinking>inner</thinking>still outer</thinking>visible", "still outer</thinking>visible"),
	Entry("unclosed opening tag", "hello<thinking>this is unclosed", "hello<thinking>this is unclosed"),
	Entry("only closing tag", "hello</thinking>world", "hello</thinking>world"),
	Entry("tags with whitespace around content", "before<thinking> spaced out </thinking>after", "beforeafter"),
	Entry("empty thinking block", "before<thinking></thinking>after", "beforeafter"),
	Entry("multiline thinking block", "before<thinking>\nline1\nline2\n</thinking>after", "beforeafter"),
	Entry("adjacent tag pairs", "<thinking>a</thinking><thinking>b</thinking>", ""),
)

var _ = Describe("ExecuteChatWithLLM", func() {
	var (
		ctx context.Context
		sc  *statusCollector
		cb  Callbacks
	)

	BeforeEach(func() {
		ctx = context.Background()
		sc = &statusCollector{}
		cb = Callbacks{
			OnStatus: sc.onStatus,
		}
	})

	Context("basic chat completion", func() {
		It("returns the LLM response", func() {
			llm := &mockLLM{response: "Hello from the agent!"}
			cfg := &AgentConfig{
				Name:  "test-agent",
				Model: "test-model",
			}

			result, err := ExecuteChatWithLLM(ctx, llm, cfg, "Hi there", cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("Hello from the agent!"))

			statuses := sc.get()
			Expect(statuses).To(ContainElement("processing"))
			Expect(statuses).To(ContainElement("completed"))
		})
	})

	Context("empty model name", func() {
		It("still succeeds because ExecuteChatWithLLM receives a pre-built LLM", func() {
			// ExecuteChatWithLLM does not check cfg.Model — that's ExecuteChat's job.
			// Verify it does not error with an empty model name.
			llm := &mockLLM{response: "ok"}
			cfg := &AgentConfig{
				Name:  "no-model-agent",
				Model: "",
			}

			result, err := ExecuteChatWithLLM(ctx, llm, cfg, "test", cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("ok"))
		})
	})

	Context("ExecuteChat rejects empty model", func() {
		It("returns an error when model is empty", func() {
			cfg := &AgentConfig{
				Name:  "empty-model",
				Model: "",
			}

			_, err := ExecuteChat(ctx, "http://localhost", "key", cfg, "test", cb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no model configured"))
		})
	})

	Context("StripThinkingTags flag", func() {
		It("strips thinking tags from the response when enabled", func() {
			llm := &mockLLM{response: "before<thinking>secret</thinking>after"}
			cfg := &AgentConfig{
				Name:              "strip-agent",
				Model:             "test-model",
				StripThinkingTags: true,
			}

			result, err := ExecuteChatWithLLM(ctx, llm, cfg, "test", cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("beforeafter"))
		})

		It("preserves thinking tags when flag is disabled", func() {
			llm := &mockLLM{response: "before<thinking>secret</thinking>after"}
			cfg := &AgentConfig{
				Name:              "no-strip-agent",
				Model:             "test-model",
				StripThinkingTags: false,
			}

			result, err := ExecuteChatWithLLM(ctx, llm, cfg, "test", cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("before<thinking>secret</thinking>after"))
		})
	})

	Context("OnMessage callback", func() {
		It("delivers the agent response via OnMessage", func() {
			var msgSender, msgContent string
			cb.OnMessage = func(sender, content, messageID string) {
				msgSender = sender
				msgContent = content
			}

			llm := &mockLLM{response: "agent reply"}
			cfg := &AgentConfig{
				Name:  "msg-agent",
				Model: "test-model",
			}

			_, err := ExecuteChatWithLLM(ctx, llm, cfg, "hello", cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(msgSender).To(Equal("agent"))
			Expect(msgContent).To(Equal("agent reply"))
		})
	})

	Context("context cancellation", func() {
		It("returns an error when context is already cancelled", func() {
			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel() // immediately cancel

			llm := &mockLLM{response: "should not reach"}
			cfg := &AgentConfig{
				Name:  "cancel-agent",
				Model: "test-model",
			}

			_, err := ExecuteChatWithLLM(cancelledCtx, llm, cfg, "test", cb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agent execution failed"))
		})
	})
})
