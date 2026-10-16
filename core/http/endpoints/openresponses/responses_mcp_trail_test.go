// SPDX-License-Identifier: MIT
package openresponses

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	trailToolName   = "get_balance"
	trailToolResult = "balance: 4711 EUR"
	trailAnswer     = "Your balance is 4711 EUR."
	trailFollowUp   = "And in USD?"
)

// trailBackend stubs model inference: with tools offered and no tool result
// yet it asks for get_balance (as pre-parsed ChatDeltas, the way the C++
// autoparser delivers them), once the tool result is in the history it
// answers, and without tools (the follow-up turn) it just answers. Every
// call records the messages it was given, which is what the backend would
// render into the prompt.
type trailBackend struct {
	mu    sync.Mutex
	calls [][]schema.Message
	// toolReasoning, when set, is emitted as a <think> block before the
	// tool call, so the trail also has to carry that iteration's reasoning.
	toolReasoning  string
	alwaysCallTool bool
}

func (b *trailBackend) install() {
	original := backend.ModelInferenceFunc
	DeferCleanup(func() { backend.ModelInferenceFunc = original })
	backend.ModelInferenceFunc = func(
		ctx context.Context, prompt string, messages schema.Messages,
		images, videos, audios []string, loader *model.ModelLoader,
		cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
		tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
		logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
	) (func() (backend.LLMResponse, error), error) {
		b.mu.Lock()
		b.calls = append(b.calls, append([]schema.Message(nil), messages...))
		b.mu.Unlock()
		wantTool := tools != "" && (b.alwaysCallTool || len(messages) == 0 || messages[len(messages)-1].Role != "tool")
		return func() (backend.LLMResponse, error) {
			usage := backend.TokenUsage{Prompt: 5, Completion: 7}
			if wantTool {
				deltas := []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Index: 0, Id: "call_1", Name: trailToolName, Arguments: `{"account":"main"}`}}}}
				text := ""
				if b.toolReasoning != "" {
					text = "<think>" + b.toolReasoning + "</think>"
					if tokenCallback != nil {
						tokenCallback(text, backend.TokenUsage{})
					}
				}
				return backend.LLMResponse{Response: text, ChatDeltas: deltas, Usage: usage}, nil
			}
			text := trailAnswer
			if tools == "" {
				text = "About 5000 USD."
			}
			if tokenCallback != nil {
				tokenCallback(text, backend.TokenUsage{})
			}
			return backend.LLMResponse{Response: text, Usage: usage}, nil
		}, nil
	}
}

func (b *trailBackend) callsSnapshot() [][]schema.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]schema.Message(nil), b.calls...)
}

// newTrailExecutor serves a real MCP tool over an in-memory transport and
// wraps it in the same LocalToolExecutor the endpoint builds from model config.
func newTrailExecutor(calls *atomic.Int32) mcpTools.ToolExecutor {
	ctx, cancel := context.WithCancel(context.Background())
	srv := mcp.NewServer(&mcp.Implementation{Name: "bank", Version: "v0"}, nil)
	type balanceArgs struct {
		Account string `json:"account"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: trailToolName, Description: "Current account balance"},
		func(_ context.Context, _ *mcp.CallToolRequest, args balanceArgs) (*mcp.CallToolResult, any, error) {
			calls.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: trailToolResult}}}, nil, nil
		})
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, t1, nil)
	Expect(err).ToNot(HaveOccurred())
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
		cancel()
	})
	exec := mcpTools.NewLocalToolExecutor(ctx, []mcpTools.NamedSession{{Name: "bank", Type: "remote", Session: clientSession}})
	Expect(exec.HasTools()).To(BeTrue())
	return exec
}

// trailRequest mirrors what ResponsesEndpoint prepares when a model has MCP
// servers configured: the discovered tools are added to the request tools.
func trailRequest(ctx context.Context, exec mcpTools.ToolExecutor, stream bool) (*schema.OpenResponsesRequest, *schema.OpenAIRequest) {
	input := &schema.OpenResponsesRequest{Model: "test-model", Input: "What is my balance?", Stream: stream, Context: ctx}
	var funcs []schema.ORFunctionTool
	if exec != nil {
		discovered, err := exec.DiscoverTools(ctx)
		Expect(err).ToNot(HaveOccurred())
		for _, fn := range discovered {
			funcs = append(funcs, schema.ORFunctionTool{Type: "function", Name: fn.Name, Description: fn.Description, Parameters: fn.Parameters})
		}
	}
	input.Tools = funcs
	messages, err := convertORInputToMessages(input.Input, &config.ModelConfig{})
	Expect(err).ToNot(HaveOccurred())
	return input, &schema.OpenAIRequest{Context: ctx, Messages: messages}
}

func trailContext() (echo.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	return echo.New().NewContext(request, recorder), recorder
}

func completedFromSSE(body string) *schema.ORResponseResource {
	var completed *schema.ORResponseResource
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var event schema.ORStreamEvent
		Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event)).To(Succeed())
		Expect(event.Type).NotTo(Equal("error"))
		if event.Type == "response.completed" {
			completed = event.Response
		}
	}
	return completed
}

func itemTypes(items []schema.ORItemField) []string {
	types := make([]string, 0, len(items))
	for _, it := range items {
		types = append(types, it.Type)
	}
	return types
}

func outputText(item schema.ORItemField) string {
	raw, err := json.Marshal(item.Content)
	Expect(err).ToNot(HaveOccurred())
	var parts []schema.ORContentPart
	Expect(json.Unmarshal(raw, &parts)).To(Succeed())
	var text string
	for _, p := range parts {
		text += p.Text
	}
	return text
}

func outputString(item schema.ORItemField) string {
	if s, ok := item.Output.(string); ok {
		return s
	}
	raw, _ := json.Marshal(item.Output)
	return string(raw)
}

// expectTrail checks the order the next turn depends on: the tool call, its
// result for the same call id, then the answer built on it.
func expectTrail(output []schema.ORItemField) {
	Expect(itemTypes(output)).To(Equal([]string{"function_call", "function_call_output", "message"}))
	expectToolPair(output[0], output[1])
	Expect(outputText(output[2])).To(Equal(trailAnswer))
}

// expectReasonedTrail is expectTrail for an iteration that reasoned before
// calling the tool: the reasoning stays in front of the call it led to.
func expectReasonedTrail(output []schema.ORItemField, reasoning string) {
	Expect(itemTypes(output)).To(Equal([]string{"reasoning", "function_call", "function_call_output", "message"}))
	Expect(outputText(output[0])).To(Equal(reasoning))
	expectToolPair(output[1], output[2])
	Expect(outputText(output[3])).To(Equal(trailAnswer))
}

func expectToolPair(call, result schema.ORItemField) {
	Expect(call.Type).To(Equal("function_call"))
	Expect(call.Name).To(Equal(trailToolName))
	Expect(call.Arguments).To(MatchJSON(`{"account":"main"}`))
	Expect(call.CallID).NotTo(BeEmpty())
	Expect(result.Type).To(Equal("function_call_output"))
	Expect(result.CallID).To(Equal(call.CallID))
	Expect(outputString(result)).To(ContainSubstring("4711"))
}

// followUp sends a second turn through the real endpoint with
// previous_response_id and returns the messages the backend received.
func followUp(be *trailBackend, previousID string) []schema.Message {
	c, recorder := trailContext()
	input := &schema.OpenResponsesRequest{Model: "test-model", Input: trailFollowUp, PreviousResponseID: previousID, Context: c.Request().Context()}
	c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
	c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "test-model"})
	before := len(be.callsSnapshot())
	handler := ResponsesEndpoint(nil, nil, templates.NewEvaluator(""), config.NewApplicationConfig(), nil)
	Expect(handler(c)).To(Succeed())
	Expect(recorder.Code).To(Equal(200), recorder.Body.String())
	calls := be.callsSnapshot()
	Expect(calls).To(HaveLen(before + 1))
	DeferCleanup(func() {
		var resp schema.ORResponseResource
		if json.Unmarshal(recorder.Body.Bytes(), &resp) == nil {
			GetGlobalStore().Delete(resp.ID)
		}
	})
	return calls[len(calls)-1]
}

// expectReplayed checks that the follow-up prompt carries the earlier tool
// call and its result, so the model can see where the numbers came from.
func expectReplayed(messages []schema.Message, previous []schema.ORItemField) {
	Expect(messages).To(HaveLen(5))
	Expect(messages[0].Role).To(Equal("user"))
	Expect(messages[1].Role).To(Equal("assistant"))
	Expect(messages[1].ToolCalls).To(HaveLen(1))
	Expect(messages[1].ToolCalls[0].ID).To(Equal(previous[0].CallID))
	Expect(messages[1].ToolCalls[0].FunctionCall.Name).To(Equal(trailToolName))
	Expect(messages[2].Role).To(Equal("tool"))
	Expect(messages[2].ToolCallID).To(Equal(previous[0].CallID))
	Expect(messages[2].StringContent).To(ContainSubstring("4711"))
	Expect(messages[3].Role).To(Equal("assistant"))
	Expect(messages[3].StringContent).To(Equal(trailAnswer))
	Expect(messages[4].Role).To(Equal("user"))
	Expect(messages[4].StringContent).To(Equal(trailFollowUp))
}

var _ = Describe("Responses keep the server-side MCP tool trail", func() {
	var (
		be        *trailBackend
		toolCalls atomic.Int32
		exec      mcpTools.ToolExecutor
		cfg       *config.ModelConfig
		evaluator *templates.Evaluator
	)

	BeforeEach(func() {
		be = &trailBackend{}
		be.install()
		toolCalls.Store(0)
		exec = newTrailExecutor(&toolCalls)
		cfg = &config.ModelConfig{Name: "test-model"}
		evaluator = templates.NewEvaluator("")
	})

	storedOutput := func(id string) []schema.ORItemField {
		stored, err := GetGlobalStore().Get(id)
		Expect(err).ToNot(HaveOccurred())
		return stored.Response.Output
	}

	It("returns, stores and replays the trail on the synchronous non-streaming path", func() {
		c, recorder := trailContext()
		input, openAIReq := trailRequest(c.Request().Context(), exec, false)
		responseID := "resp_trail_nonstream"
		DeferCleanup(func() { GetGlobalStore().Delete(responseID) })

		Expect(handleOpenResponsesNonStream(c, responseID, 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, true, exec, evaluator, 0, nil)).To(Succeed())
		Expect(toolCalls.Load()).To(Equal(int32(1)))

		var resp schema.ORResponseResource
		Expect(json.Unmarshal(recorder.Body.Bytes(), &resp)).To(Succeed())
		expectTrail(resp.Output)
		expectTrail(storedOutput(responseID))

		expectReplayed(followUp(be, responseID), resp.Output)
	})

	It("returns, stores and replays the trail on the synchronous streaming path", func() {
		c, recorder := trailContext()
		input, openAIReq := trailRequest(c.Request().Context(), exec, true)
		responseID := "resp_trail_stream"
		DeferCleanup(func() { GetGlobalStore().Delete(responseID) })

		Expect(handleOpenResponsesStream(c, responseID, 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, true, exec, evaluator)).To(Succeed())
		Expect(toolCalls.Load()).To(Equal(int32(1)))

		completed := completedFromSSE(recorder.Body.String())
		Expect(completed).NotTo(BeNil())
		expectTrail(completed.Output)
		expectTrail(storedOutput(responseID))

		expectReplayed(followUp(be, responseID), completed.Output)
	})

	Context("when the model reasons before calling the tool", func() {
		const reasoning = "Need the current balance."

		BeforeEach(func() { be.toolReasoning = reasoning })

		It("keeps that reasoning in front of the call on the non-streaming path", func() {
			c, recorder := trailContext()
			input, openAIReq := trailRequest(c.Request().Context(), exec, false)
			responseID := "resp_trail_reasoned"
			DeferCleanup(func() { GetGlobalStore().Delete(responseID) })

			Expect(handleOpenResponsesNonStream(c, responseID, 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, true, exec, evaluator, 0, nil)).To(Succeed())
			var resp schema.ORResponseResource
			Expect(json.Unmarshal(recorder.Body.Bytes(), &resp)).To(Succeed())
			expectReasonedTrail(resp.Output, reasoning)
			expectReasonedTrail(storedOutput(responseID), reasoning)

			replayed := followUp(be, responseID)
			Expect(replayed[1].Reasoning).NotTo(BeNil())
			Expect(*replayed[1].Reasoning).To(Equal(reasoning))
			Expect(replayed[1].ToolCalls).To(HaveLen(1))
		})

		It("keeps that reasoning in front of the call on the streaming path", func() {
			c, recorder := trailContext()
			input, openAIReq := trailRequest(c.Request().Context(), exec, true)
			responseID := "resp_trail_reasoned_stream"
			DeferCleanup(func() { GetGlobalStore().Delete(responseID) })

			Expect(handleOpenResponsesStream(c, responseID, 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, true, exec, evaluator)).To(Succeed())
			completed := completedFromSSE(recorder.Body.String())
			Expect(completed).NotTo(BeNil())
			expectReasonedTrail(completed.Output, reasoning)
			expectReasonedTrail(storedOutput(responseID), reasoning)

			replayed := followUp(be, responseID)
			Expect(replayed[1].Reasoning).NotTo(BeNil())
			Expect(*replayed[1].Reasoning).To(Equal(reasoning))
			Expect(replayed[1].ToolCalls).To(HaveLen(1))
		})
	})

	It("keeps each executed round once when streaming exhausts the iteration limit", func() {
		be.alwaysCallTool = true
		be.toolReasoning = "Need the current balance."
		cfg.Agent.MaxIterations = 1
		c, recorder := trailContext()
		input, openAIReq := trailRequest(c.Request().Context(), exec, true)
		responseID := "resp_trail_limit"
		DeferCleanup(func() { GetGlobalStore().Delete(responseID) })
		Expect(handleOpenResponsesStream(c, responseID, 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, true, exec, evaluator)).To(Succeed())
		completed := completedFromSSE(recorder.Body.String())
		Expect(completed).NotTo(BeNil())
		Expect(toolCalls.Load()).To(Equal(int32(2)))
		for _, output := range [][]schema.ORItemField{completed.Output, storedOutput(responseID)} {
			Expect(itemTypes(output)).To(Equal([]string{"reasoning", "function_call", "function_call_output", "reasoning", "function_call", "function_call_output"}))
			expectToolPair(output[1], output[2])
			expectToolPair(output[4], output[5])
			ids := map[string]bool{}
			for _, item := range output {
				Expect(ids[item.ID]).To(BeFalse(), "duplicate item ID: %s", item.ID)
				ids[item.ID] = true
			}
		}
	})

	Context("counter-checks", func() {
		It("leaves a non-streaming request without tools at a single message", func() {
			c, recorder := trailContext()
			input, openAIReq := trailRequest(c.Request().Context(), nil, false)
			Expect(handleOpenResponsesNonStream(c, "resp_trail_plain", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, false, false, nil, evaluator, 0, nil)).To(Succeed())
			var resp schema.ORResponseResource
			Expect(json.Unmarshal(recorder.Body.Bytes(), &resp)).To(Succeed())
			Expect(itemTypes(resp.Output)).To(Equal([]string{"message"}))
			Expect(outputText(resp.Output[0])).To(Equal("About 5000 USD."))
			Expect(toolCalls.Load()).To(BeZero())
		})

		It("leaves a streaming request without tools at a single message", func() {
			c, recorder := trailContext()
			input, openAIReq := trailRequest(c.Request().Context(), nil, true)
			Expect(handleOpenResponsesStream(c, "resp_trail_plain_stream", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, false, false, nil, evaluator)).To(Succeed())
			completed := completedFromSSE(recorder.Body.String())
			Expect(completed).NotTo(BeNil())
			Expect(itemTypes(completed.Output)).To(Equal([]string{"message"}))
			Expect(toolCalls.Load()).To(BeZero())
		})

		It("keeps the trail on the background non-streaming path as before", func() {
			ctx := context.Background()
			input, openAIReq := trailRequest(ctx, exec, false)
			resp, err := handleBackgroundNonStream(ctx, NewResponseStore(0), "resp_trail_bg", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, exec, evaluator)
			Expect(err).ToNot(HaveOccurred())
			// Pinned as found: the tool pair leads the output. This path
			// adds the plain-text answer only when the backend parses it
			// into ChatDeltas or no earlier item exists, so the stubbed
			// answer is not asserted here.
			Expect(len(resp.Output)).To(BeNumerically(">=", 2))
			expectToolPair(resp.Output[0], resp.Output[1])
			Expect(toolCalls.Load()).To(Equal(int32(1)))
		})

		It("keeps the trail on the background streaming path as before", func() {
			ctx := context.Background()
			store := NewResponseStore(0)
			input, openAIReq := trailRequest(ctx, exec, true)
			store.StoreBackground("resp_trail_bg_stream", input, buildORResponse("resp_trail_bg_stream", 1, nil, schema.ORStatusQueued, input, nil, nil, true), func() {}, true)
			resp, err := handleBackgroundStream(ctx, store, "resp_trail_bg_stream", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "", openAIReq, nil, true, true, exec, evaluator)
			Expect(err).ToNot(HaveOccurred())
			// Pinned as found: an empty message item opened before the
			// tool call, then the tool pair, then the answer.
			Expect(itemTypes(resp.Output)).To(Equal([]string{"message", "function_call", "function_call_output", "message"}))
			expectToolPair(resp.Output[1], resp.Output[2])
			Expect(outputText(resp.Output[3])).To(Equal(trailAnswer))
			Expect(toolCalls.Load()).To(Equal(int32(1)))
		})
	})
})
