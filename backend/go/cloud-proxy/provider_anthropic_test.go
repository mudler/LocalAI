package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/gomega"
)

// fakeAnthropicUpstream mirrors fakeOpenAIUpstream but decodes the
// request body as an anthropicRequest so tests can assert on the
// translated wire shape (system field, max_tokens, etc.).
func fakeAnthropicUpstream(t *testing.T, handler func(req anthropicRequest) (status int, body string, contentType string)) (*httptest.Server, *anthropicRequest) {
	t.Helper()
	var captured anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		status, body, ct := handler(captured)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	return srv, &captured
}

func newAnthropicTranslateCloudProxy(t *testing.T, upstreamURL string) *CloudProxy {
	t.Helper()
	g := NewWithT(t)
	t.Setenv("CLOUD_PROXY_ANTHROPIC_FAKE", "sk-ant-fake")
	cp := NewCloudProxy()
	err := cp.Load(&pb.ModelOptions{
		Model: "claude-local",
		Proxy: &pb.ProxyOptions{
			UpstreamUrl:   upstreamURL,
			Mode:          modeTranslate,
			Provider:      providerAnthropic,
			ApiKeyEnv:     "CLOUD_PROXY_ANTHROPIC_FAKE",
			UpstreamModel: "claude-3-5-sonnet-20241022",
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	return cp
}

func TestPredict_Anthropic_BasicMessages(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi there"}],"model":"claude-3-5-sonnet-20241022","usage":{"input_tokens":5,"output_tokens":2}}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	got, err := cp.Predict(&pb.PredictOptions{
		Messages: []*pb.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
		},
		Temperature: 0.5,
		TopP:        0.9,
		Tokens:      32,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal("hi there"))

	g.Expect(captured.Model).To(Equal("claude-3-5-sonnet-20241022"))
	// System message must be hoisted out of Messages into top-level field.
	g.Expect(captured.System).To(Equal("be brief"))
	g.Expect(captured.Messages).To(HaveLen(1))
	g.Expect(captured.Messages[0].Role).To(Equal("user"))
	g.Expect(captured.MaxTokens).To(Equal(int32(32)))
	// Newer Anthropic reasoning models reject requests carrying temperature
	// ("`temperature` is deprecated for this model"); clients typically send
	// only default sampling values, so the translator forwards neither.
	g.Expect(captured.Temperature).To(BeNil())
	g.Expect(captured.TopP).To(BeNil())
	g.Expect(captured.Stream).To(BeFalse())
}

// Sampling parameters are not forwarded at all — the upstream applies its
// own defaults (newest models reject explicit temperature/top_p).
func TestPredict_Anthropic_TopPOnly(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[{"type":"text","text":"ok"}]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	_, err := cp.Predict(&pb.PredictOptions{
		Messages: []*pb.Message{{Role: "user", Content: "hello"}},
		TopP:     0.9,
		Tokens:   16,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.Temperature).To(BeNil())
	g.Expect(captured.TopP).To(BeNil())
}

func TestPredict_Anthropic_DefaultsMaxTokens(t *testing.T) {
	g := NewWithT(t)
	// Anthropic 400s without max_tokens. The translator must default
	// it when the caller doesn't supply Tokens.
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[{"type":"text","text":"ok"}]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	_, err := cp.Predict(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.MaxTokens).To(Equal(anthropicDefaultMaxTokens))
}

func TestPredict_Anthropic_PromptFallback(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[{"type":"text","text":"ok"}]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	_, err := cp.Predict(&pb.PredictOptions{Prompt: "what time is it?", Tokens: 16})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.Messages).To(HaveLen(1))
	g.Expect(captured.Messages[0].Role).To(Equal("user"))
	g.Expect(captured.Messages[0].Content).To(Equal("what time is it?"))
}

func TestPredict_Anthropic_ConcatenatesContentBlocks(t *testing.T) {
	g := NewWithT(t)
	// Anthropic may return multiple text blocks; the translator joins
	// them so the Predict() string return is the full assistant message.
	srv, _ := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	got, err := cp.Predict(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}, Tokens: 16})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal("hello world"))
}

func TestPredict_Anthropic_UpstreamError(t *testing.T) {
	g := NewWithT(t)
	srv, _ := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 401, `{"error":{"type":"authentication_error","message":"bad key"}}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	_, err := cp.Predict(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}, Tokens: 16})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("401"))
}

func TestPredictStream_Anthropic_StreamsTextDeltas(t *testing.T) {
	g := NewWithT(t)
	// Real Anthropic SSE has event: lines + data: lines. The translator
	// only needs the data: payload; only content_block_delta with
	// delta.type=text_delta carries content. message_stop ends.
	frames := []string{
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" \"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"world\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	body := strings.Join(frames, "")

	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, body, "text/event-stream"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	results := make(chan string, 8)
	done := make(chan error, 1)
	go func() {
		done <- cp.PredictStream(&pb.PredictOptions{
			Messages: []*pb.Message{{Role: "user", Content: "hi"}},
			Tokens:   16,
		}, results)
	}()

	var got []string
	for s := range results {
		got = append(got, s)
	}
	err := <-done
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(strings.Join(got, "")).To(Equal("hello world"))
	g.Expect(captured.Stream).To(BeTrue())
}

func TestBuildAnthropic_TranslatesOpenAITools(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[{"type":"text","text":"ok"}]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	tools := `[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]`
	_, err := cp.Predict(&pb.PredictOptions{
		Messages:   []*pb.Message{{Role: "user", Content: "weather in Paris?"}},
		Tools:      tools,
		ToolChoice: `"auto"`,
		Tokens:     32,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.Tools).To(HaveLen(1))
	g.Expect(captured.Tools[0].Name).To(Equal("get_weather"))
	g.Expect(captured.Tools[0].Description).To(Equal("Get weather"))
	// input_schema must be the parameters object verbatim.
	g.Expect(string(captured.Tools[0].InputSchema)).To(ContainSubstring(`"city"`))
	g.Expect(captured.ToolChoice).NotTo(BeNil())
	g.Expect(captured.ToolChoice.Type).To(Equal("auto"))
}

func TestBuildAnthropic_ToolChoice_RequiredMapsToAny(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)
	_, err := cp.Predict(&pb.PredictOptions{
		Messages:   []*pb.Message{{Role: "user", Content: "x"}},
		Tools:      `[{"type":"function","function":{"name":"t","parameters":{"type":"object"}}}]`,
		ToolChoice: `"required"`,
		Tokens:     16,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.ToolChoice).NotTo(BeNil())
	g.Expect(captured.ToolChoice.Type).To(Equal("any"))
}

func TestBuildAnthropic_ToolChoice_NoneDropsTools(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)
	_, err := cp.Predict(&pb.PredictOptions{
		Messages:   []*pb.Message{{Role: "user", Content: "x"}},
		Tools:      `[{"type":"function","function":{"name":"t","parameters":{"type":"object"}}}]`,
		ToolChoice: `"none"`,
		Tokens:     16,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.Tools).To(BeNil())
	g.Expect(captured.ToolChoice).To(BeNil())
}

func TestBuildAnthropic_ToolChoice_NamedFunction(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)
	_, err := cp.Predict(&pb.PredictOptions{
		Messages:   []*pb.Message{{Role: "user", Content: "x"}},
		Tools:      `[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]`,
		ToolChoice: `{"type":"function","function":{"name":"weather"}}`,
		Tokens:     16,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.ToolChoice).NotTo(BeNil())
	g.Expect(captured.ToolChoice.Type).To(Equal("tool"))
	g.Expect(captured.ToolChoice.Name).To(Equal("weather"))
}

func TestBuildAnthropic_RoundTripsAssistantToolCalls(t *testing.T) {
	g := NewWithT(t)
	// LocalAI Assistant's second turn: the LLM previously emitted a
	// tool_use, the server executed it, and the conversation now
	// includes the assistant turn (with tool_calls) plus a tool-role
	// result message. Both must convert to Anthropic block form.
	srv, captured := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{"content":[{"type":"text","text":"ok"}]}`, "application/json"
	})
	defer srv.Close()
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	tools := `[{"type":"function","function":{"name":"list_models","parameters":{"type":"object"}}}]`
	toolCallsJSON := `[{"id":"call_abc","type":"function","function":{"name":"list_models","arguments":"{}"}}]`
	_, err := cp.Predict(&pb.PredictOptions{
		Tools: tools,
		Messages: []*pb.Message{
			{Role: "user", Content: "what models are installed?"},
			{Role: "assistant", Content: "", ToolCalls: toolCallsJSON},
			{Role: "tool", Content: `{"models":["a","b"]}`, ToolCallId: "call_abc"},
		},
		Tokens: 64,
	})
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(captured.Messages).To(HaveLen(3))
	// 1. user text — bare string
	s, ok := captured.Messages[0].Content.(string)
	g.Expect(ok).To(BeTrue())
	g.Expect(s).To(Equal("what models are installed?"))
	// 2. assistant — must be a content-block list with one tool_use
	// json.Unmarshal of `any` produces []any not []anthropicContentBlock.
	blocks, ok := captured.Messages[1].Content.([]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(blocks).To(HaveLen(1))
	b0, _ := blocks[0].(map[string]any)
	g.Expect(b0["type"]).To(Equal("tool_use"))
	g.Expect(b0["id"]).To(Equal("call_abc"))
	g.Expect(b0["name"]).To(Equal("list_models"))
	// 3. tool → user with tool_result block
	g.Expect(captured.Messages[2].Role).To(Equal("user"))
	resBlocks, _ := captured.Messages[2].Content.([]any)
	r0, _ := resBlocks[0].(map[string]any)
	g.Expect(r0["type"]).To(Equal("tool_result"))
	g.Expect(r0["tool_use_id"]).To(Equal("call_abc"))
	g.Expect(r0["content"]).To(Equal(`{"models":["a","b"]}`))
}
