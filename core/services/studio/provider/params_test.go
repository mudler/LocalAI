package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func userRequest(text string) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{{Role: "user", Content: text}}}
}

func floatp(f float64) *float64 { return &f }
func intp(i int) *int           { return &i }

var _ = Describe("LLMWith", func() {
	It("adds the chat settings to the request", func() {
		var (
			mu      sync.Mutex
			gotBody map[string]any
			gotHdr  string
		)
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			defer mu.Unlock()
			_ = json.Unmarshal(raw, &gotBody)
			gotHdr = r.Header.Get("Extra-Usage")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":120,"completion_tokens":30,"timing_token_generation":1000}}`)
		}))

		var used provider.Usage
		llm := p.LLMWith(nil, "m", provider.Params{
			Temperature: floatp(0.25), TopP: floatp(0.5), TopK: intp(7), ReasoningEffort: "high",
			OnUsage: func(u provider.Usage) { mu.Lock(); used = u; mu.Unlock() },
		})
		_, _, err := llm.CreateChatCompletion(context.Background(), userRequest("hello"))
		Expect(err).ToNot(HaveOccurred(), "CreateChatCompletion")

		mu.Lock()
		defer mu.Unlock()
		for key, want := range map[string]any{"temperature": 0.25, "top_p": 0.5, "top_k": float64(7), "reasoning_effort": "high"} {
			Expect(gotBody).To(HaveKeyWithValue(key, want), "request %s (body %v)", key, gotBody)
		}
		Expect(gotHdr).ToNot(BeEmpty(), "Extra-Usage header not set, so the route would leave out the timings")
		Expect(used.PromptTokens).To(Equal(120))
		Expect(used.CompletionTokens).To(Equal(30))
		Expect(used.TokensPerSecond).To(BeNumerically("==", 30))
	})

	It("asks for stream usage and reports it from the last chunk", func() {
		var sawOption bool
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			sawOption = strings.Contains(string(raw), `"include_usage":true`)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":10}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}))
		got := make(chan provider.Usage, 1)
		llm := p.LLMWith(nil, "m", provider.Params{OnUsage: func(u provider.Usage) { got <- u }})
		streaming, ok := llm.(cogito.StreamingLLM)
		Expect(ok).To(BeTrue(), "the LocalAI client does not stream")
		stream, err := streaming.CreateChatCompletionStream(context.Background(), userRequest("hello"))
		Expect(err).ToNot(HaveOccurred(), "CreateChatCompletionStream")
		for range stream {
		}
		Expect(sawOption).To(BeTrue(), "the stream request did not ask for include_usage")
		u := <-got
		Expect(u.PromptTokens).To(Equal(50))
		Expect(u.CompletionTokens).To(Equal(10))
		Expect(u.Node).To(BeEmpty(), "no node header, so no node")
	})

	It("reports the prompt time and the node that served the request", func() {
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-LocalAI-Node", "worker-2")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":80,"completion_tokens":20,"timing_prompt_processing":250,"timing_token_generation":500}}`)
		}))
		got := make(chan provider.Usage, 1)
		llm := p.LLMWith(nil, "m", provider.Params{OnUsage: func(u provider.Usage) { got <- u }})
		_, _, err := llm.CreateChatCompletion(context.Background(), userRequest("hello"))
		Expect(err).ToNot(HaveOccurred(), "CreateChatCompletion")
		u := <-got
		Expect(u.PromptMillis).To(BeNumerically("==", 250))
		Expect(u.TokensPerSecond).To(BeNumerically("==", 40))
		Expect(u.Node).To(Equal("worker-2"))
	})
})
