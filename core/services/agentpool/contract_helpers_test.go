package agentpool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agentpool"
	. "github.com/onsi/gomega"
)

type fakeLLMRequest struct {
	Model    string
	Stream   bool
	Messages []map[string]any
}

// fakeLLM is an OpenAI-compatible chat endpoint. The standalone pool reaches its
// LLM over HTTP (apiURL), so a real server is the only seam that exercises the
// whole agent loop without a model.
type fakeLLM struct {
	srv      *httptest.Server
	mu       sync.Mutex
	reply    string
	requests []fakeLLMRequest
}

func newFakeLLM(reply string) *fakeLLM {
	f := &fakeLLM{reply: reply}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeLLM) URL() string { return f.srv.URL }
func (f *fakeLLM) Close()      { f.srv.Close() }

func (f *fakeLLM) SetReply(r string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = r
}

func (f *fakeLLM) Requests() []fakeLLMRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeLLMRequest(nil), f.requests...)
}

func (f *fakeLLM) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Model    string           `json:"model"`
		Stream   bool             `json:"stream"`
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)

	f.mu.Lock()
	f.requests = append(f.requests, fakeLLMRequest{Model: req.Model, Stream: req.Stream, Messages: req.Messages})
	reply := f.reply
	f.mu.Unlock()

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-fake", "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-fake", "object": "chat.completion.chunk", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	chunk(map[string]any{"role": "assistant", "content": reply}, nil)
	chunk(map[string]any{}, "stop")
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
}

// startStandalone boots a real standalone AgentPoolService on stateDir with its
// LLM pointed at llmURL. It registers no cleanup itself: callers own Stop().
func startStandalone(stateDir, llmURL string) *agentpool.AgentPoolService {
	cfg := config.NewApplicationConfig()
	cfg.AgentPool = config.AgentPoolConfig{
		Enabled:      true,
		StateDir:     stateDir,
		APIURL:       llmURL,
		DefaultModel: "fake-model",
		Timeout:      "30s",
	}
	svc, err := agentpool.NewAgentPoolService(cfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(svc.Start(context.Background())).To(Succeed())
	return svc
}

func newAgentConfig(name string) *state.AgentConfig {
	return &state.AgentConfig{
		Name:         name,
		Model:        "fake-model",
		Description:  "contract test agent",
		SystemPrompt: "You are a test agent.",
	}
}
