// SPDX-License-Identifier: MIT
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"net/http/httptest"
	"regexp"
	"sync"

	"github.com/mudler/LocalAI/core/schema"
	d "github.com/mudler/LocalAI/pkg/diagnostics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stream dispatch boundaries", func() {
	It("distinguishes empty, role-only, reasoning-first and content chunks without changing bytes or flushes", func() {
		text, empty := "private sentinel", ""
		chunks := []schema.OpenAIResponse{
			{Choices: []schema.Choice{{Delta: &schema.Message{Content: &empty}}}},
			{Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}}}},
			{Choices: []schema.Choice{{Delta: &schema.Message{Reasoning: &text}}}},
			{Choices: []schema.Choice{{Delta: &schema.Message{Content: &text}}}},
		}
		var events []d.Event
		ctx := d.NewRecorder(func(e d.Event) { events = append(events, e) }).Request(context.Background())
		observed := newStreamDiagnostics(ctx)
		disabled := newStreamDiagnostics(context.Background())
		flushes := 0
		for _, chunk := range chunks {
			before, err := json.Marshal(chunk)
			Expect(err).NotTo(HaveOccurred())
			disabled.chunk(chunk)
			observed.chunk(chunk)
			observed.chunk(chunk)
			observed.flush(func() { flushes++ })
			after, err := json.Marshal(chunk)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		}
		Expect(flushes).To(Equal(len(chunks)))
		d.FlushDispatch(ctx)
		var phases []d.Phase
		for _, e := range events {
			if e.Phase != d.PhaseDispatchSummary {
				phases = append(phases, e.Phase)
			}
		}
		Expect(phases).To(Equal([]d.Phase{d.PhaseSSEFlush, d.PhaseSSEFlush, d.PhaseSSERole, d.PhaseSSEReasoning, d.PhaseSSEContent}))
	})
	It("records request-wide milestones once with racing producers and ignores tool arguments", func() {
		var events []d.Event
		ctx := d.NewRecorder(func(e d.Event) { events = append(events, e) }).Request(context.Background())
		observer := newStreamDiagnostics(ctx)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				observer.chunk(schema.OpenAIResponse{Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant", ToolCalls: []schema.ToolCall{{FunctionCall: schema.FunctionCall{Arguments: "private"}}}}}}})
			}()
		}
		wg.Wait()
		d.FlushDispatch(ctx)
		Expect(events).To(HaveLen(2))
		Expect(events[0].Phase).To(Equal(d.PhaseSSERole))
	})
})

// Capture flush positions as well as bytes: equal flush counts alone can hide
// buffering changes. IDs and wall-clock creation times vary between requests.
type diagnosticWriter struct {
	*httptest.ResponseRecorder
	flushPositions []int
	failAt, writes int
}

func (w *diagnosticWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.failAt != 0 && w.writes == w.failAt {
		return 0, errDiagnosticWrite
	}
	return w.ResponseRecorder.Write(p)
}
func (w *diagnosticWriter) Flush() {
	w.flushPositions = append(w.flushPositions, w.Body.Len())
	w.ResponseRecorder.Flush()
}

var errDiagnosticWrite = errors.New("fixture write failure")

var _ = Describe("Chat endpoint diagnostic equivalence", func() {
	DescribeTable("preserves SSE bytes and flush positions", func(mode string) {
		original := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = original })
		run := func(enabled bool) (string, []int, []d.Event) {
			var mu sync.Mutex
			var events []d.Event
			ctx := context.Background()
			if enabled {
				ctx = d.NewRecorder(func(e d.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }).Request(ctx)
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			finished := make(chan struct{})
			cancelHandled := make(chan struct{})
			var cancelOnce sync.Once
			backend.ModelInferenceFunc = func(ctx context.Context, s string, messages schema.Messages, images, videos, audios []string, loader *model.ModelLoader, cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig, callback func(string, backend.TokenUsage) bool, tools, toolChoice string, logprobs, topLogprobs *int, bias map[string]float64, metadata map[string]string) (func() (backend.LLMResponse, error), error) {
				return func() (backend.LLMResponse, error) {
					defer close(finished)
					response := "hello"
					usage := backend.TokenUsage{Prompt: 3, Completion: 2}
					switch mode {
					case "reasoning":
						callback("", backend.TokenUsage{ChatDeltas: []*pb.ChatDelta{{ReasoningContent: "reason"}}})
						callback("", backend.TokenUsage{ChatDeltas: []*pb.ChatDelta{{Content: "hello"}}})
					case "empty":
						callback("", usage)
					case "deferred tools":
						return backend.LLMResponse{ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Id: "tool-id", Name: "search", Arguments: "{}"}}}}, Usage: usage}, nil
					case "fallback":
						response = `{"name":"search","arguments":{}}`
						callback(response, usage)
					case "no action":
						return backend.LLMResponse{Response: `{"name":"answer","arguments":{"message":"hello"}}`, Usage: usage}, nil
					case "structured":
						response = `{"value":1}`
						callback(response, usage)
					case "cancel":
						// Pin cancellation before producer completion. If both
						// are ready, the existing handler can consume ended
						// then wait for it again during cancellation cleanup.
						// This fixture tests diagnostic equivalence in one
						// interleaving, not general cancellation safety.
						callback("hello", usage)
						cancel()
						<-cancelHandled
					case "write failure":
						callback("hello", usage)
						<-ctx.Done()
						d.Dispatch(ctx).MarkOnce(d.PhaseFrontendCallback, d.OutcomeOK)
					case "error before":
						return backend.LLMResponse{}, errors.New("fixture")
					case "error after":
						callback("hello", usage)
						return backend.LLMResponse{}, errors.New("fixture")
					default:
						callback(response, usage)
					}
					return backend.LLMResponse{Response: response, Usage: usage}, nil
				}, nil
			}
			input := &schema.OpenAIRequest{Context: ctx, Cancel: func() { cancel(); cancelOnce.Do(func() { close(cancelHandled) }) }}
			input.Model = "fixture"
			input.Stream = true
			cfg := &config.ModelConfig{}
			cfg.TemplateConfig.UseTokenizerTemplate = true
			budget := 2
			cfg.Maxtokens = &budget
			cfg.FunctionsConfig.GrammarConfig.NoGrammar = true
			if mode == "deferred tools" || mode == "no action" {
				input.Functions = functions.Functions{{Name: "search"}}
			}
			if mode == "fallback" {
				cfg.FunctionsConfig.AutomaticToolParsingFallback = true
			}
			if mode == "structured" {
				cfg.ResponseFormatMap = map[string]any{"type": "json_object"}
			}
			writer := &diagnosticWriter{ResponseRecorder: httptest.NewRecorder()}
			if mode == "write failure" {
				writer.failAt = 2
			}
			c := echo.New().NewContext(httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx), writer)
			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
			err := ChatEndpoint(nil, nil, nil, config.NewApplicationConfig(), nil, nil, nil)(c)
			if mode == "write failure" {
				Expect(err).To(MatchError(errDiagnosticWrite))
			} else if mode == "error before" {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			Eventually(finished).Should(BeClosed())
			if enabled {
				Eventually(func() bool {
					mu.Lock()
					defer mu.Unlock()
					for _, e := range events {
						if (len(writer.flushPositions) == 0 && e.Phase == d.PhaseDispatchSummary) || (e.Phase == d.PhaseSSEFlush && e.State == d.StateEnd) {
							return true
						}
					}
					return false
				}).Should(BeTrue())
				if mode == "reasoning" {
					Eventually(func() bool {
						mu.Lock()
						defer mu.Unlock()
						for _, e := range events {
							if e.Phase == d.PhaseSSEContent {
								return true
							}
						}
						return false
					}).Should(BeTrue())
				}
				if mode == "write failure" {
					Eventually(func() bool {
						mu.Lock()
						defer mu.Unlock()
						for _, e := range events {
							if e.Phase == d.PhaseFrontendCallback {
								return true
							}
						}
						return false
					}).Should(BeTrue())
				}
			}
			// Match request-unique fields only. All other SSE bytes remain untouched.
			body := regexp.MustCompile(`"id":"[0-9a-f-]{36}(?:-[0-9]+)?"`).ReplaceAllString(writer.Body.String(), `"id":"00000000-0000-0000-0000-000000000000"`)
			body = regexp.MustCompile(`"created":[0-9]+`).ReplaceAllString(body, `"created":0000000000`)
			mu.Lock()
			defer mu.Unlock()
			return body, writer.flushPositions, append([]d.Event(nil), events...)
		}
		plain, pf, _ := run(false)
		observed, of, events := run(true)
		Expect(observed).To(Equal(plain))
		Expect(of).To(Equal(pf))
		counts := map[d.Phase]int{}
		for _, e := range events {
			if e.State == d.StateMark {
				counts[e.Phase]++
			}
		}
		for _, phase := range []d.Phase{d.PhaseSSERole, d.PhaseSSEContent, d.PhaseSSEReasoning} {
			Expect(counts[phase]).To(BeNumerically("<=", 1))
		}
		if mode == "reasoning" {
			Expect(counts[d.PhaseSSEReasoning]).To(Equal(1))
			Expect(counts[d.PhaseSSEContent]).To(Equal(1))
		}
		if mode == "empty" {
			Expect(counts[d.PhaseSSERole]).To(Equal(1))
			Expect(counts[d.PhaseSSEContent]).To(BeZero())
		}
	}, Entry("plain", "plain"), Entry("empty", "empty"), Entry("reasoning-first structured deltas", "reasoning"), Entry("deferred tools", "deferred tools"), Entry("automatic fallback", "fallback"), Entry("no action fallback", "no action"), Entry("structured output", "structured"), Entry("cancel", "cancel"), Entry("write failure with late producer", "write failure"), Entry("error before SSE", "error before"), Entry("error after SSE", "error after"))
})
