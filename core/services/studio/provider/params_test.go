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
	})

	It("sends the images of the last user message only", func() {
		var got map[string]any
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		}))
		image := func(text, uri string) openai.ChatCompletionMessage {
			return openai.ChatCompletionMessage{Role: "user", MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeText, Text: text},
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: uri}},
			}}
		}
		req := openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: "be brief"},
			image("what is this?", "data:image/png;base64,T0xE"),
			{Role: "assistant", Content: "a cat"},
			image("and this?", "data:image/png;base64,TkVX"),
			// The tool loop of the turn: the last user message keeps its image.
			{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "1", Type: "function", Function: openai.FunctionCall{Name: "search", Arguments: "{}"}}}},
			{Role: "tool", ToolCallID: "1", Content: "nothing"},
		}}
		llm := p.LLMWith(nil, "m", provider.Params{OnlyLastImages: true})
		_, _, err := llm.CreateChatCompletion(context.Background(), req)
		Expect(err).ToNot(HaveOccurred())

		msgs := got["messages"].([]any)
		Expect(msgs).To(HaveLen(6))
		older, _ := json.Marshal(msgs[1])
		Expect(string(older)).ToNot(ContainSubstring("image_url"), "an older image was sent again: %s", older)
		Expect(string(older)).ToNot(ContainSubstring("T0xE"))
		Expect(msgs[1]).To(HaveKeyWithValue("content", []any{
			map[string]any{"type": "text", "text": "what is this?"},
			map[string]any{"type": "text", "text": provider.OlderImageNote},
		}))
		last, _ := json.Marshal(msgs[3])
		Expect(string(last)).To(ContainSubstring(`"url":"data:image/png;base64,TkVX"`))
	})

	It("leaves every image in place without OnlyLastImages", func() {
		var raw []byte
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		}))
		part := func(uri string) openai.ChatCompletionMessage {
			return openai.ChatCompletionMessage{Role: "user", MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: uri}},
			}}
		}
		req := openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{part("data:image/png;base64,T0xE"), part("data:image/png;base64,TkVX")}}
		_, _, err := p.LLMWith(nil, "m", provider.Params{TopK: intp(1)}).CreateChatCompletion(context.Background(), req)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("T0xE"))
		Expect(string(raw)).To(ContainSubstring("TkVX"))
	})
})
