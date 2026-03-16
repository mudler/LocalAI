package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type modelInferenceFunc = func(
	ctx context.Context, s string, messages schema.Messages,
	images, videos, audios []string,
	loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
	o *config.ApplicationConfig,
	tokenCallback func(string, backend.TokenUsage) bool,
	tools, toolChoice string,
	logprobs, topLogprobs *int,
	logitBias map[string]float64,
	metadata map[string]string,
) (func() (backend.LLMResponse, error), error)

var _ = Describe("ComputeChoices", func() {
	var (
		origInference modelInferenceFunc
		cfg           *config.ModelConfig
		appCfg        *config.ApplicationConfig
	)

	// mockInference installs a stub that yields the given responses sequentially.
	// After all responses are consumed, the last one is repeated.
	mockInference := func(responses []backend.LLMResponse) {
		idx := 0
		backend.ModelInferenceFunc = func(
			ctx context.Context, s string, messages schema.Messages,
			images, videos, audios []string,
			loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
			o *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool,
			tools, toolChoice string,
			logprobs, topLogprobs *int,
			logitBias map[string]float64,
			metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			predFunc := func() (backend.LLMResponse, error) {
				resp := responses[idx]
				if idx < len(responses)-1 {
					idx++
				}
				return resp, nil
			}
			return predFunc, nil
		}
	}

	BeforeEach(func() {
		origInference = backend.ModelInferenceFunc
		cfg = &config.ModelConfig{}
		appCfg = config.NewApplicationConfig()
	})

	AfterEach(func() {
		backend.ModelInferenceFunc = origInference
	})

	makeReq := func() *schema.OpenAIRequest {
		ctx, cancel := context.WithCancel(context.Background())
		_ = cancel
		return &schema.OpenAIRequest{
			Context: ctx,
			Cancel:  cancel,
		}
	}

	Context("normal response (no retry needed)", func() {
		It("should return choices on first attempt", func() {
			mockInference([]backend.LLMResponse{
				{Response: "Hello world", Usage: backend.TokenUsage{Prompt: 10, Completion: 5}},
			})

			var captured string
			choices, usage, _, err := ComputeChoices(
				makeReq(), "test prompt", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					captured = s
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(captured).To(Equal("Hello world"))
			Expect(usage.Prompt).To(Equal(10))
			Expect(usage.Completion).To(Equal(5))
		})
	})

	Context("empty response triggers built-in retry", func() {
		It("should retry and eventually return non-empty response", func() {
			mockInference([]backend.LLMResponse{
				{Response: ""},   // attempt 0: empty
				{Response: "  "}, // attempt 1: whitespace-only
				{Response: "Got it", Usage: backend.TokenUsage{Prompt: 8, Completion: 3}}, // attempt 2: success
			})

			choices, usage, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(choices[0].Text).To(Equal("Got it"))
			Expect(usage.Prompt).To(Equal(8))
			Expect(usage.Completion).To(Equal(3))
		})
	})

	Context("all retries exhausted on empty response", func() {
		It("should return the empty response after max retries", func() {
			mockInference([]backend.LLMResponse{
				{Response: ""}, // always empty
			})

			choices, _, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
			)
			Expect(err).ToNot(HaveOccurred())
			// After maxRetries, it proceeds with the empty response
			Expect(choices).To(HaveLen(1))
			Expect(choices[0].Text).To(BeEmpty())
		})
	})

	Context("shouldRetry callback", func() {
		It("should call shouldRetry and retry when it returns true", func() {
			callCount := 0
			mockInference([]backend.LLMResponse{
				{Response: "reasoning-only", Usage: backend.TokenUsage{Prompt: 5, Completion: 2}},
				{Response: "actual-answer", Usage: backend.TokenUsage{Prompt: 5, Completion: 4}},
			})

			retryAttempts := []int{}
			choices, usage, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					callCount++
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
				func(attempt int) bool {
					retryAttempts = append(retryAttempts, attempt)
					// Retry on first attempt only
					return attempt == 0
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(choices[0].Text).To(Equal("actual-answer"))
			// shouldRetry was called twice: once returning true (retry), once returning false (proceed)
			Expect(retryAttempts).To(Equal([]int{0, 1}))
			// cb was called twice (once per attempt)
			Expect(callCount).To(Equal(2))
			// Token usage should be from the LATEST attempt
			Expect(usage.Prompt).To(Equal(5))
			Expect(usage.Completion).To(Equal(4))
		})

		It("should not retry when shouldRetry returns false", func() {
			mockInference([]backend.LLMResponse{
				{Response: "first-response"},
			})

			shouldRetryCalled := false
			choices, _, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
				func(attempt int) bool {
					shouldRetryCalled = true
					return false
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(choices[0].Text).To(Equal("first-response"))
			Expect(shouldRetryCalled).To(BeTrue())
		})
	})

	Context("shouldRetry not provided (variadic omitted)", func() {
		It("should work without shouldRetry parameter", func() {
			mockInference([]backend.LLMResponse{
				{Response: "works"},
			})

			choices, _, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(choices[0].Text).To(Equal("works"))
		})
	})

	Context("token usage from latest attempt", func() {
		It("should use token usage from the last attempt, not accumulated", func() {
			mockInference([]backend.LLMResponse{
				{Response: "retry-me", Usage: backend.TokenUsage{Prompt: 100, Completion: 50}},
				{Response: "final", Usage: backend.TokenUsage{Prompt: 10, Completion: 5}},
			})

			_, usage, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
				func(attempt int) bool { return attempt == 0 },
			)
			Expect(err).ToNot(HaveOccurred())
			// Should be the LATEST attempt's usage, not accumulated
			Expect(usage.Prompt).To(Equal(10))
			Expect(usage.Completion).To(Equal(5))
		})
	})

	Context("chat deltas from latest attempt", func() {
		It("should return chat deltas from the last attempt only", func() {
			mockInference([]backend.LLMResponse{
				{
					Response:   "retry-me",
					ChatDeltas: []*pb.ChatDelta{{Content: "old"}},
				},
				{
					Response:   "final",
					ChatDeltas: []*pb.ChatDelta{{Content: "new"}},
				},
			})

			_, _, deltas, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
				func(attempt int) bool { return attempt == 0 },
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(deltas).To(HaveLen(1))
			Expect(deltas[0].Content).To(Equal("new"))
		})
	})

	Context("result choices cleared on retry", func() {
		It("should only contain choices from the final attempt", func() {
			mockInference([]backend.LLMResponse{
				{Response: "bad-choice"},
				{Response: "good-choice"},
			})

			choices, _, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
				func(attempt int) bool { return attempt == 0 },
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(choices[0].Text).To(Equal("good-choice"))
		})
	})

	Context("shouldRetry with max retries cap", func() {
		It("should stop retrying after maxRetries even if shouldRetry returns true", func() {
			attempts := 0
			mockInference([]backend.LLMResponse{
				{Response: "always-retry"},
			})

			choices, _, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
				func(attempt int) bool {
					attempts++
					return true // always want to retry
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			// maxRetries is 5, so shouldRetry is called for attempts 0..4,
			// but attempt 5 is the final one where shouldRetry can't trigger continue
			Expect(attempts).To(BeNumerically("<=", 6))
		})
	})

	Context("N > 1 completions", func() {
		It("should produce N separate completions", func() {
			callIdx := 0
			responses := []string{"first", "second", "third"}
			backend.ModelInferenceFunc = func(
				ctx context.Context, s string, messages schema.Messages,
				images, videos, audios []string,
				loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
				o *config.ApplicationConfig,
				tokenCallback func(string, backend.TokenUsage) bool,
				tools, toolChoice string,
				logprobs, topLogprobs *int,
				logitBias map[string]float64,
				metadata map[string]string,
			) (func() (backend.LLMResponse, error), error) {
				predFunc := func() (backend.LLMResponse, error) {
					resp := backend.LLMResponse{Response: responses[callIdx]}
					if callIdx < len(responses)-1 {
						callIdx++
					}
					return resp, nil
				}
				return predFunc, nil
			}

			req := makeReq()
			req.N = 3
			choices, _, _, err := ComputeChoices(
				req, "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(3))
			Expect(choices[0].Text).To(Equal("first"))
			Expect(choices[1].Text).To(Equal("second"))
			Expect(choices[2].Text).To(Equal("third"))
		})
	})

	Context("with streaming token callback", func() {
		It("should call tokenCallback for streaming responses", func() {
			var streamedTokens []string
			backend.ModelInferenceFunc = func(
				ctx context.Context, s string, messages schema.Messages,
				images, videos, audios []string,
				loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
				o *config.ApplicationConfig,
				tokenCallback func(string, backend.TokenUsage) bool,
				tools, toolChoice string,
				logprobs, topLogprobs *int,
				logitBias map[string]float64,
				metadata map[string]string,
			) (func() (backend.LLMResponse, error), error) {
				predFunc := func() (backend.LLMResponse, error) {
					if tokenCallback != nil {
						tokenCallback("Hello", backend.TokenUsage{Prompt: 5})
						tokenCallback(" world", backend.TokenUsage{Prompt: 5, Completion: 2})
					}
					return backend.LLMResponse{
						Response: "Hello world",
						Usage:    backend.TokenUsage{Prompt: 5, Completion: 2},
					}, nil
				}
				return predFunc, nil
			}

			choices, _, _, err := ComputeChoices(
				makeReq(), "test", cfg, nil, appCfg, nil,
				func(s string, c *[]schema.Choice) {
					*c = append(*c, schema.Choice{Text: s})
				},
				func(s string, usage backend.TokenUsage) bool {
					streamedTokens = append(streamedTokens, s)
					return true
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(choices).To(HaveLen(1))
			Expect(streamedTokens).To(Equal([]string{"Hello", " world"}))
		})
	})
})
