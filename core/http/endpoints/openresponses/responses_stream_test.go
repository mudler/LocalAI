// SPDX-License-Identifier: MIT
package openresponses

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Responses stream item consistency", func() {
	DescribeTable("preserves every item and its announced output index", func(tokens []string, chatDeltas []*pb.ChatDelta, wantReasoning, wantAnswer string, fallback bool) {
		originalInference := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = originalInference })
		backend.ModelInferenceFunc = func(
			ctx context.Context, prompt string, messages schema.Messages,
			images, videos, audios []string, loader *model.ModelLoader,
			cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
			logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			return func() (backend.LLMResponse, error) {
				for i, token := range tokens {
					usage := backend.TokenUsage{}
					if len(chatDeltas) > 0 {
						usage.ChatDeltas = []*pb.ChatDelta{chatDeltas[i]}
					}
					if !tokenCallback(token, usage) {
						break
					}
				}
				return backend.LLMResponse{Response: strings.Join(tokens, ""), ChatDeltas: chatDeltas, Usage: backend.TokenUsage{Prompt: 3, Completion: 8}}, nil
			}, nil
		}
		cfg := &config.ModelConfig{}
		cfg.FunctionsConfig.AutomaticToolParsingFallback = fallback
		cfg.FunctionsConfig.JSONRegexMatch = []string{`(?s)<tool_call>(.*?)</tool_call>`}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/v1/responses", nil)
		c := echo.New().NewContext(request, recorder)
		input := &schema.OpenResponsesRequest{Model: "test-model", Input: "hello", Stream: true}
		err := handleOpenResponsesStream(c, "resp_test", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "hello", &schema.OpenAIRequest{Context: request.Context()}, nil, false, false, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(recorder.Body.String()).To(HaveSuffix("data: [DONE]\n\n"))

		var events []schema.ORStreamEvent
		var completed *schema.ORResponseResource
		for _, line := range strings.Split(recorder.Body.String(), "\n") {
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			var event schema.ORStreamEvent
			Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event)).To(Succeed())
			Expect(event.Type).NotTo(Equal("error"))
			events = append(events, event)
			if event.Type == "response.completed" {
				completed = event.Response
			}
		}
		Expect(completed).NotTo(BeNil())
		wantCount := 1
		if wantReasoning != "" {
			wantCount++
		}
		if fallback {
			wantCount++
		}
		Expect(completed.Output).To(HaveLen(wantCount), "final output must retain the answer alongside reasoning and fallback calls")

		indices := map[string]int{}
		done := map[string]int{}
		deltas := map[string]string{}
		for i, event := range events {
			Expect(event.SequenceNumber).To(Equal(i))
			if event.Type == "response.output_item.added" {
				Expect(event.Item).NotTo(BeNil())
				Expect(event.OutputIndex).NotTo(BeNil())
				Expect(indices).NotTo(HaveKey(event.Item.ID))
				Expect(*event.OutputIndex).To(Equal(len(indices)))
				indices[event.Item.ID] = *event.OutputIndex
			}
			id := event.ItemID
			if event.Item != nil {
				id = event.Item.ID
			}
			if id == "" {
				continue
			}
			Expect(indices).To(HaveKey(id))
			Expect(event.OutputIndex).NotTo(BeNil())
			Expect(*event.OutputIndex).To(Equal(indices[id]), "event %s changes the index for %s", event.Type, id)
			Expect(completed.Output[indices[id]].ID).To(Equal(id))
			if event.Type == "response.output_item.done" {
				done[id]++
				Expect(event.Item.Status).To(Equal("completed"))
				Expect(event.Item.Type).To(Equal(completed.Output[indices[id]].Type))
				if event.Item.Type == "function_call" {
					Expect(event.Item.Name).To(Equal(completed.Output[indices[id]].Name))
					Expect(event.Item.Arguments).To(Equal(completed.Output[indices[id]].Arguments))
				} else {
					Expect(event.Item.Content).To(Equal(completed.Output[indices[id]].Content))
				}
			}
			if event.Type == "response.output_text.delta" {
				deltas[id] += *event.Delta
			}
		}
		Expect(indices).To(HaveLen(wantCount))
		for _, item := range completed.Output {
			Expect(done[item.ID]).To(Equal(1))
			switch item.Type {
			case "message", "reasoning":
				want := wantAnswer
				if item.Type == "reasoning" {
					want = wantReasoning
				}
				parts, ok := item.Content.([]any)
				Expect(ok).To(BeTrue())
				Expect(parts).To(HaveLen(1))
				Expect(parts[0].(map[string]any)["text"]).To(Equal(want))
				if !fallback {
					Expect(deltas[item.ID]).To(Equal(want))
				}
			case "function_call":
				Expect(item.Name).To(Equal("get_weather"))
				Expect(item.Arguments).To(MatchJSON(`{"city":"Rome"}`))
				Expect(item.CallID).NotTo(BeEmpty())
			default:
				Fail("unexpected output item type: " + item.Type)
			}
		}
	},
		Entry("tagged reasoning and answer", []string{"<think>", "Let me think.", "</think>", "The answer is 42."}, nil, "Let me think.", "The answer is 42.", false),
		Entry("backend reasoning and answer deltas", []string{"", ""}, []*pb.ChatDelta{{ReasoningContent: "Let me think."}, {Content: "The answer is 42."}}, "Let me think.", "The answer is 42.", false),
		Entry("plain text", []string{"Hello", " world."}, nil, "", "Hello world.", false),
		Entry("automatic fallback tool call", []string{`<tool_call>{"name":"get_weather","arguments":{"city":"Rome"}}</tool_call>`}, nil, "", "", true),
		Entry("reasoning and automatic fallback tool call", []string{"<think>", "Let me think.", "</think>", `<tool_call>{"name":"get_weather","arguments":{"city":"Rome"}}</tool_call>`}, nil, "Let me think.", "", true),
	)
})
