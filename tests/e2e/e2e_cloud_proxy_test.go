package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Cloud-proxy e2e tests drive real HTTP requests through LocalAI ->
// cloud-proxy backend (separate process) -> fake upstream httptest
// server. The whole pipeline is exercised: chat handler dispatch,
// gRPC client/server, cloud-proxy translation, upstream call,
// response forwarding back to the client.
var _ = Describe("Cloud-proxy backend E2E", func() {
	BeforeEach(func() {
		if cloudProxyPath == "" {
			Skip("cloud-proxy backend binary not built (make build-cloud-proxy-backend)")
		}
		// Reset upstream scripts + counters between specs so a previous
		// spec's hits don't leak in. The default script is restored by
		// each spec that needs a custom one.
		cpOpenAIUpstream.SetScript(defaultOpenAIScript)
		cpAnthropicUpstream.SetScript(defaultAnthropicScript)
	})

	Context("Passthrough mode — OpenAI shape", func() {
		It("forwards a chat completion request verbatim and pipes the response back", func() {
			cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) {
				return 200, `{"id":"resp-pt","choices":[{"index":0,"message":{"role":"assistant","content":"hi via passthrough"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`, "application/json"
			})

			cp := openai.NewClient(option.WithBaseURL(apiURL))
			resp, err := cp.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
				Model: "cp-passthrough-openai",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("hello"),
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Choices).NotTo(BeEmpty())
			Expect(resp.Choices[0].Message.Content).To(Equal("hi via passthrough"))

			// Upstream observed an Authorization header sourced from
			// the api_key_env we set at suite startup.
			_, _, hdr, _ := cpOpenAIUpstream.Snapshot()
			Expect(hdr.Get("Authorization")).To(Equal("Bearer sk-e2e-openai"))
			// Body field assertions prove the wire format wasn't
			// rewritten — passthrough mode shouldn't touch tools,
			// messages, etc.
			body := cpOpenAIUpstream.DecodedBody()
			Expect(body["messages"]).NotTo(BeNil())
		})
	})

	Context("Passthrough mode — Anthropic shape", func() {
		It("forwards an Anthropic Messages request with x-api-key + anthropic-version", func() {
			cpAnthropicUpstream.SetScript(func([]byte) (int, string, string) {
				return 200, `{"id":"msg-pt","type":"message","role":"assistant","content":[{"type":"text","text":"hi via passthrough anthropic"}],"model":"claude","usage":{"input_tokens":4,"output_tokens":6}}`, "application/json"
			})

			// Anthropic SDK omitted to keep the test self-contained;
			// raw POST exercises the same path. The Anthropic endpoint
			// is /v1/messages on LocalAI.
			reqBody := `{"model":"cp-passthrough-anthropic","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
			httpResp, err := http.Post(anthropicBaseURL+"/v1/messages", "application/json", strings.NewReader(reqBody))
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = httpResp.Body.Close() }()
			Expect(httpResp.StatusCode).To(Equal(200))

			respBody, _ := io.ReadAll(httpResp.Body)
			Expect(string(respBody)).To(ContainSubstring("hi via passthrough anthropic"))

			_, _, hdr, _ := cpAnthropicUpstream.Snapshot()
			Expect(hdr.Get("x-api-key")).To(Equal("sk-ant-e2e"))
			Expect(hdr.Get("anthropic-version")).NotTo(BeEmpty())
			Expect(hdr.Get("Authorization")).To(BeEmpty(), "Authorization leaked on anthropic backend")
		})
	})

	Context("Translate mode — OpenAI provider", func() {
		// The chat handler only emits tool_calls in the response when
		// the client asked for tools. The translate backend forwards
		// whatever the upstream returns, but the endpoint-level
		// assembly is gated on the request shape — same as for local
		// models. The e2e tests therefore declare tools on the
		// outbound request so the response-side assembly fires.
		toolsParam := []openai.ChatCompletionToolUnionParam{
			openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
				Name:        "lookup",
				Description: openai.String("look something up"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"q": map[string]any{"type": "string"},
					},
				},
			}),
		}

		It("delivers tool_calls in the chat completion response", func() {
			cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) {
				return nonStreamingOpenAIToolCallScript()
			})

			cp := openai.NewClient(option.WithBaseURL(apiURL))
			resp, err := cp.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
				Model: "cp-translate-openai",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("find clouds"),
				},
				Tools: toolsParam,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Choices).NotTo(BeEmpty())
			tcs := resp.Choices[0].Message.ToolCalls
			Expect(tcs).To(HaveLen(1), "tool_calls should survive translate-mode round-trip")
			Expect(tcs[0].Function.Name).To(Equal("lookup"))
			Expect(tcs[0].Function.Arguments).To(ContainSubstring(`"q":"clouds"`))
			// Token usage propagated from upstream.
			Expect(resp.Usage.PromptTokens).To(BeNumerically(">", 0))
		})

		It("streams tool_call deltas through SSE", func() {
			cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) {
				return streamingOpenAIToolCallScript()
			})

			cp := openai.NewClient(option.WithBaseURL(apiURL))
			stream := cp.Chat.Completions.NewStreaming(context.TODO(), openai.ChatCompletionNewParams{
				Model: "cp-translate-openai",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("what's the weather in SF?"),
				},
				Tools: []openai.ChatCompletionToolUnionParam{
					openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
						Name:        "get_weather",
						Description: openai.String("look up the weather"),
						Parameters: openai.FunctionParameters{
							"type": "object",
							"properties": map[string]any{
								"location": map[string]any{"type": "string"},
							},
						},
					}),
				},
			})

			var toolID, toolName string
			var args strings.Builder
			for stream.Next() {
				chunk := stream.Current()
				for _, ch := range chunk.Choices {
					for _, tc := range ch.Delta.ToolCalls {
						if tc.ID != "" {
							toolID = tc.ID
						}
						if tc.Function.Name != "" {
							toolName = tc.Function.Name
						}
						args.WriteString(tc.Function.Arguments)
					}
				}
			}
			Expect(stream.Err()).NotTo(HaveOccurred())
			Expect(toolID).To(Equal("call_e2e"))
			Expect(toolName).To(Equal("get_weather"))
			// Argument fragments assembled in order.
			var parsed map[string]any
			Expect(json.Unmarshal([]byte(args.String()), &parsed)).To(Succeed())
			Expect(parsed["location"]).To(Equal("SF"))
		})
	})

	Context("Translate mode — Anthropic provider", func() {
		It("preserves tool_use blocks through Messages API", func() {
			cpAnthropicUpstream.SetScript(func([]byte) (int, string, string) {
				return 200, `{"id":"msg-tu","type":"message","role":"assistant","content":[{"type":"text","text":"Let me check"},{"type":"tool_use","id":"toolu_e2e","name":"weather","input":{"location":"SF"}}],"model":"claude","usage":{"input_tokens":7,"output_tokens":12}}`, "application/json"
			})

			// Anthropic Messages endpoint exposes tool_use blocks
			// directly. Raw POST + JSON decode keeps the test
			// independent of any specific SDK version's accessor API.
			// Tools declared on the request so the response-side
			// assembly populates the tool_use blocks (same gate as
			// for local models).
			reqBody := `{"model":"cp-translate-anthropic","max_tokens":64,"messages":[{"role":"user","content":"what's the weather?"}],"tools":[{"name":"weather","description":"weather lookup","input_schema":{"type":"object","properties":{"location":{"type":"string"}}}}]}`
			httpResp, err := http.Post(anthropicBaseURL+"/v1/messages", "application/json", strings.NewReader(reqBody))
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = httpResp.Body.Close() }()
			Expect(httpResp.StatusCode).To(Equal(200))

			var decoded map[string]any
			Expect(json.NewDecoder(httpResp.Body).Decode(&decoded)).To(Succeed())
			contentArr, ok := decoded["content"].([]any)
			Expect(ok).To(BeTrue(), "response must carry content array")
			var sawToolUse bool
			for _, block := range contentArr {
				m := block.(map[string]any)
				if m["type"] == "tool_use" {
					sawToolUse = true
					Expect(m["name"]).To(Equal("weather"))
					// Anthropic content-block assembly synthesizes
					// tool_use IDs from the LocalAI request ID rather
					// than passing through the upstream's toolu_* ID
					// (see messages.go:253-267). Documenting the
					// current behavior — the synthesized ID still
					// follows the toolu_ prefix convention so SDK
					// validation passes.
					id, _ := m["id"].(string)
					Expect(id).To(HavePrefix("toolu_"))
					input, _ := m["input"].(map[string]any)
					Expect(input["location"]).To(Equal("SF"))
				}
			}
			Expect(sawToolUse).To(BeTrue(), "tool_use block must survive translate-mode round-trip")
		})
	})

	Context("Translate mode + PII filter", func() {
		It("blocks request-side secrets before they reach the upstream", func() {
			// Our NER/pattern PII tier is request-side by design: it scans
			// inbound content, not upstream responses. cp-translate-openai
			// wires the in-process pattern detector (e2e-secret-filter, block
			// action), so a leaked secret in the user's message must trip the
			// request-side filter and the request must be rejected before the
			// cloud-proxy ever forwards it to the provider.
			var upstreamHit bool
			cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) {
				upstreamHit = true
				return 200, `{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, "application/json"
			})

			cp := openai.NewClient(option.WithBaseURL(apiURL))
			_, err := cp.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
				Model: "cp-translate-openai",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("my key is " + leakedAnthropicKey + " please keep it"),
				},
			})
			// Lock-in: request-side PII fires in translate mode, the request is
			// rejected by content policy, and the secret never leaves the box.
			Expect(err).To(HaveOccurred(), "request carrying a leaked secret must be rejected")
			Expect(err.Error()).To(ContainSubstring("pii_blocked"))
			Expect(upstreamHit).To(BeFalse(), "blocked request must not reach the upstream provider")
		})
	})
})

func defaultOpenAIScript([]byte) (int, string, string) {
	return 200, `{"id":"chatcmpl-default","choices":[{"index":0,"message":{"role":"assistant","content":"default openai reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, "application/json"
}

func defaultAnthropicScript([]byte) (int, string, string) {
	return 200, `{"id":"msg-default","type":"message","role":"assistant","content":[{"type":"text","text":"default anthropic reply"}],"model":"claude","usage":{"input_tokens":1,"output_tokens":1}}`, "application/json"
}
