package backend_test

import (
	. "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LLM tests", func() {
	Context("Finetune LLM output", func() {
		var (
			testConfig config.ModelConfig
			input      string
			prediction string
			result     string
		)

		BeforeEach(func() {
			testConfig = config.ModelConfig{
				PredictionOptions: schema.PredictionOptions{
					Echo: false,
				},
				LLMConfig: config.LLMConfig{
					Cutstrings:   []string{`<.*?>`},                  // Example regex for removing XML tags
					ExtractRegex: []string{`<result>(.*?)</result>`}, // Example regex to extract from tags
					TrimSpace:    []string{" ", "\n"},
					TrimSuffix:   []string{".", "!"},
				},
			}
		})

		Context("when echo is enabled", func() {
			BeforeEach(func() {
				testConfig.Echo = true
				input = "Hello"
				prediction = "World"
			})

			It("should prepend input to prediction", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("HelloWorld"))
			})
		})

		Context("when echo is disabled", func() {
			BeforeEach(func() {
				testConfig.Echo = false
				input = "Hello"
				prediction = "World"
			})

			It("should not modify the prediction with input", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("World"))
			})
		})

		Context("when cutstrings regex is applied", func() {
			BeforeEach(func() {
				input = ""
				prediction = "<div>Hello</div> World"
			})

			It("should remove substrings matching cutstrings regex", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("Hello World"))
			})
		})

		Context("when extract regex is applied", func() {
			BeforeEach(func() {
				input = ""
				prediction = "<response><result>42</result></response>"
			})

			It("should extract substrings matching the extract regex", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("42"))
			})
		})

		Context("when trimming spaces", func() {
			BeforeEach(func() {
				input = ""
				prediction = "   Hello World   "
			})

			It("should trim spaces from the prediction", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("Hello World"))
			})
		})

		Context("when trimming suffixes", func() {
			BeforeEach(func() {
				input = ""
				prediction = "Hello World."
			})

			It("should trim suffixes from the prediction", func() {
				result = Finetune(testConfig, input, prediction)
				Expect(result).To(Equal("Hello World"))
			})
		})
	})
})

var _ = Describe("TokenUsage ChatDelta helpers", func() {
	Describe("HasChatDeltaContent", func() {
		It("should return false when ChatDeltas is nil", func() {
			usage := TokenUsage{}
			Expect(usage.HasChatDeltaContent()).To(BeFalse())
		})

		It("should return false when ChatDeltas is empty", func() {
			usage := TokenUsage{ChatDeltas: []*pb.ChatDelta{}}
			Expect(usage.HasChatDeltaContent()).To(BeFalse())
		})

		It("should return false when all deltas have empty content and reasoning", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{Content: "", ReasoningContent: ""},
					{Content: ""},
				},
			}
			Expect(usage.HasChatDeltaContent()).To(BeFalse())
		})

		It("should return true when a delta has content", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{Content: "hello"},
				},
			}
			Expect(usage.HasChatDeltaContent()).To(BeTrue())
		})

		It("should return true when a delta has reasoning content", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{ReasoningContent: "thinking..."},
				},
			}
			Expect(usage.HasChatDeltaContent()).To(BeTrue())
		})

		It("should return true when a delta has both content and reasoning", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{Content: "hello", ReasoningContent: "thinking..."},
				},
			}
			Expect(usage.HasChatDeltaContent()).To(BeTrue())
		})
	})

	Describe("ChatDeltaReasoningAndContent", func() {
		It("should return empty strings when ChatDeltas is nil", func() {
			usage := TokenUsage{}
			reasoning, content := usage.ChatDeltaReasoningAndContent()
			Expect(reasoning).To(BeEmpty())
			Expect(content).To(BeEmpty())
		})

		It("should concatenate content from multiple deltas", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{Content: "Hello"},
					{Content: " world"},
				},
			}
			reasoning, content := usage.ChatDeltaReasoningAndContent()
			Expect(content).To(Equal("Hello world"))
			Expect(reasoning).To(BeEmpty())
		})

		It("should concatenate reasoning from multiple deltas", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{ReasoningContent: "step 1"},
					{ReasoningContent: " step 2"},
				},
			}
			reasoning, content := usage.ChatDeltaReasoningAndContent()
			Expect(reasoning).To(Equal("step 1 step 2"))
			Expect(content).To(BeEmpty())
		})

		It("should separate reasoning and content from mixed deltas", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{ReasoningContent: "thinking"},
					{Content: "answer"},
				},
			}
			reasoning, content := usage.ChatDeltaReasoningAndContent()
			Expect(reasoning).To(Equal("thinking"))
			Expect(content).To(Equal("answer"))
		})

		It("should handle deltas with both fields set", func() {
			usage := TokenUsage{
				ChatDeltas: []*pb.ChatDelta{
					{Content: "a", ReasoningContent: "r1"},
					{Content: "b", ReasoningContent: "r2"},
				},
			}
			reasoning, content := usage.ChatDeltaReasoningAndContent()
			Expect(reasoning).To(Equal("r1r2"))
			Expect(content).To(Equal("ab"))
		})
	})
})
