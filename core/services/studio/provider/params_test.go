package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func userRequest(text string) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{{Role: "user", Content: text}}}
}

func floatp(f float64) *float64 { return &f }
func intp(i int) *int           { return &i }

func TestLLMWithAddsTheChatSettingsToTheRequest(t *testing.T) {
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
	if _, _, err := llm.CreateChatCompletion(context.Background(), userRequest("hello")); err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for key, want := range map[string]any{"temperature": 0.25, "top_p": 0.5, "top_k": float64(7), "reasoning_effort": "high"} {
		if gotBody[key] != want {
			t.Errorf("request %s = %v, want %v (body %v)", key, gotBody[key], want, gotBody)
		}
	}
	if gotHdr == "" {
		t.Error("Extra-Usage header not set, so the route would leave out the timings")
	}
	if used.PromptTokens != 120 || used.CompletionTokens != 30 || used.TokensPerSecond != 30 {
		t.Errorf("usage = %+v, want 120 prompt tokens, 30 completion tokens, 30 tokens/s", used)
	}
}

func TestLLMWithAsksForStreamUsageAndReportsItFromTheLastChunk(t *testing.T) {
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
	if !ok {
		t.Fatal("the LocalAI client does not stream")
	}
	stream, err := streaming.CreateChatCompletionStream(context.Background(), userRequest("hello"))
	if err != nil {
		t.Fatalf("CreateChatCompletionStream: %v", err)
	}
	for range stream {
	}
	if !sawOption {
		t.Error("the stream request did not ask for include_usage")
	}
	if u := <-got; u.PromptTokens != 50 || u.CompletionTokens != 10 {
		t.Errorf("usage = %+v, want 50 and 10", u)
	}
}
