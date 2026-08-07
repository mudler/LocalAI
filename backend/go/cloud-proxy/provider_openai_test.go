package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("OpenAI reasoning effort translation", func() {
	decodeRequest := func(metadata map[string]string) (openAIRequest, map[string]json.RawMessage) {
		body, err := buildOpenAIRequest(&pb.PredictOptions{
			Metadata: metadata,
		}, &proxyConfig{upstreamModel: "gpt-5"}, true)
		Expect(err).NotTo(HaveOccurred())

		var request openAIRequest
		Expect(json.Unmarshal(body, &request)).To(Succeed())
		var raw map[string]json.RawMessage
		Expect(json.Unmarshal(body, &raw)).To(Succeed())
		return request, raw
	}

	It("forwards the Realtime none effort to OpenAI", func() {
		request, raw := decodeRequest(map[string]string{"reasoning_effort": "none"})
		Expect(request.ReasoningEffort).To(Equal("none"))
		Expect(raw).To(HaveKeyWithValue("reasoning_effort", MatchJSON(`"none"`)))
	})

	It("maps the Realtime disable-thinking metadata to none", func() {
		request, _ := decodeRequest(map[string]string{"enable_thinking": "false"})
		Expect(request.ReasoningEffort).To(Equal("none"))
	})

	It("omits reasoning_effort when Realtime did not set it", func() {
		request, raw := decodeRequest(nil)
		Expect(request.ReasoningEffort).To(BeEmpty())
		Expect(raw).ToNot(HaveKey("reasoning_effort"))
	})

	It("omits unsupported reasoning effort metadata", func() {
		request, raw := decodeRequest(map[string]string{"reasoning_effort": "unexpected"})
		Expect(request.ReasoningEffort).To(BeEmpty())
		Expect(raw).ToNot(HaveKey("reasoning_effort"))
	})
})

// fakeOpenAIUpstream returns an httptest.Server that decodes the
// inbound request as an openAIRequest, calls handler with it, and
// writes the handler's reply as the response.
func fakeOpenAIUpstream(t *testing.T, handler func(req openAIRequest) (status int, body string, contentType string)) (*httptest.Server, *openAIRequest) {
	t.Helper()
	var captured openAIRequest
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

func newTranslateCloudProxy(t *testing.T, upstreamURL string) *CloudProxy {
	t.Helper()
	g := NewWithT(t)
	t.Setenv("CLOUD_PROXY_OPENAI_FAKE", "sk-fake-openai")
	cp := NewCloudProxy()
	err := cp.Load(&pb.ModelOptions{
		Model: "gpt-4o-local",
		Proxy: &pb.ProxyOptions{
			UpstreamUrl:   upstreamURL,
			Mode:          modeTranslate,
			Provider:      providerOpenAI,
			ApiKeyEnv:     "CLOUD_PROXY_OPENAI_FAKE",
			UpstreamModel: "gpt-4o",
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	return cp
}

func TestPredict_OpenAI_BasicChat(t *testing.T) {
	g := NewWithT(t)
	srv, captured := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, `{"id":"resp-1","choices":[{"index":0,"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`, "application/json"
	})
	defer srv.Close()
	cp := newTranslateCloudProxy(t, srv.URL)

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

	// Verify the upstream saw a properly-translated request.
	g.Expect(captured.Model).To(Equal("gpt-4o"))
	g.Expect(captured.Messages).To(HaveLen(2))
	g.Expect(captured.Messages[0].Role).To(Equal("system"))
	g.Expect(captured.Messages[1].Role).To(Equal("user"))
	// Sampling parameters are not forwarded (newest models reject explicit
	// temperature); token limit is serialized as max_completion_tokens.
	g.Expect(captured.Temperature).To(BeNil())
	g.Expect(captured.MaxTokens).NotTo(BeNil())
	g.Expect(*captured.MaxTokens).To(Equal(int32(32)))
	g.Expect(captured.Stream).To(BeFalse())
}

func TestPredict_OpenAI_PromptFallback(t *testing.T) {
	g := NewWithT(t)
	// No Messages array — backend should synth a single user message
	// from Prompt so non-chat clients still route through translate.
	srv, captured := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`, "application/json"
	})
	defer srv.Close()
	cp := newTranslateCloudProxy(t, srv.URL)

	_, err := cp.Predict(&pb.PredictOptions{Prompt: "what time is it?"})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(captured.Messages).To(HaveLen(1))
	g.Expect(captured.Messages[0].Role).To(Equal("user"))
	g.Expect(captured.Messages[0].Content).To(Equal("what time is it?"))
}

func TestPredict_OpenAI_UpstreamError(t *testing.T) {
	g := NewWithT(t)
	srv, _ := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 401, `{"error":{"message":"bad key"}}`, "application/json"
	})
	defer srv.Close()
	cp := newTranslateCloudProxy(t, srv.URL)

	_, err := cp.Predict(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("401"))
}

func TestPredictStream_OpenAI_StreamsContent(t *testing.T) {
	g := NewWithT(t)
	// Stream three content deltas then [DONE]. Verify the channel
	// receives them in order with no missing pieces.
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"world"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	body := ""
	for _, c := range chunks {
		body += "data: " + c + "\n\n"
	}
	body += "data: [DONE]\n\n"

	srv, captured := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, body, "text/event-stream"
	})
	defer srv.Close()
	cp := newTranslateCloudProxy(t, srv.URL)

	results := make(chan string, 8)
	done := make(chan error, 1)
	go func() {
		done <- cp.PredictStream(&pb.PredictOptions{
			Messages: []*pb.Message{{Role: "user", Content: "hi"}},
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

func TestPredict_RejectedInPassthroughMode(t *testing.T) {
	g := NewWithT(t)
	t.Setenv("CLOUD_PROXY_FAKE", "k")
	cp := NewCloudProxy()
	err := cp.Load(&pb.ModelOptions{
		Proxy: &pb.ProxyOptions{
			UpstreamUrl: "https://example.com",
			Mode:        modePassthrough,
			ApiKeyEnv:   "CLOUD_PROXY_FAKE",
		},
	})
	g.Expect(err).NotTo(HaveOccurred())
	_, err = cp.Predict(&pb.PredictOptions{})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("only valid in translate"))
}

func TestPredict_OpenAI_RequestTimeout(t *testing.T) {
	g := NewWithT(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
		}
	}))
	defer srv.Close()
	cp := NewCloudProxy()
	err := cp.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
		UpstreamUrl:           srv.URL,
		Mode:                  modeTranslate,
		Provider:              providerOpenAI,
		RequestTimeoutSeconds: 1,
	}})
	g.Expect(err).NotTo(HaveOccurred())
	started := time.Now()
	_, err = cp.Predict(&pb.PredictOptions{Prompt: "hi"})
	g.Expect(err).To(HaveOccurred())
	g.Expect(time.Since(started)).To(BeNumerically("<", 1800*time.Millisecond))
	g.Expect(err.Error()).To(ContainSubstring("context deadline exceeded"))
}

func TestPredictStream_OpenAI_RejectsEntirelyMalformedSSE(t *testing.T) {
	g := NewWithT(t)
	srv, _ := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return http.StatusOK, "data: {not-json}\\n\\ndata: [DONE]\\n\\n", "text/event-stream"
	})
	defer srv.Close()
	cp := newTranslateCloudProxy(t, srv.URL)
	results := make(chan string, 1)
	err := cp.PredictStream(&pb.PredictOptions{Prompt: "hi"}, results)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("malformed SSE"))
}
