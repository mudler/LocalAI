package agentpool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/mudler/LocalAGI/core/sse"
	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/LocalAGI/core/types"
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

// awaitRunning blocks until the agent's Run loop is serving jobs. The pool
// starts Run in a goroutine and LocalAGI's Scheduler.Start and Scheduler.Stop
// are unsynchronized: a Stop (update, delete, svc.Stop) that lands while Start
// is still running can nil the scheduler context under the poll goroutine and
// crash the test binary. Run starts its workers only after Scheduler.Start has
// returned, and jobQueue is unbuffered, so Execute returning proves Start is
// done. The job's context is already cancelled, so the worker finishes it as
// expired without calling the LLM or recording an observable.
func awaitRunning(svc *agentpool.AgentPoolService, userID, name string) {
	a := svc.GetAgentForUser(userID, name)
	Expect(a).ToNot(BeNil())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Execute(types.NewJob(types.WithContext(ctx)))
}

type sseEvent struct {
	Name string
	Data map[string]any
}

// collectSSE registers a listener on the agent's SSE manager and records every
// event until stop is called. Subscribe before Chat so nothing is missed.
func collectSSE(svc *agentpool.AgentPoolService, userID, name string) (events func() []sseEvent, stop func()) {
	mgr := svc.GetSSEManagerForUser(userID, name)
	Expect(mgr).ToNot(BeNil())
	client := sse.NewClient("contract-" + name)
	mgr.Register(client)

	var mu sync.Mutex
	var got []sseEvent
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case env, ok := <-client.Chan():
				if !ok {
					return
				}
				ev := sseEvent{}
				for _, line := range strings.Split(env.String(), "\n") {
					switch {
					case strings.HasPrefix(line, "event:"):
						ev.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
					case strings.HasPrefix(line, "data:"):
						_ = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev.Data)
					}
				}
				mu.Lock()
				got = append(got, ev)
				mu.Unlock()
			}
		}
	}()
	return func() []sseEvent {
			mu.Lock()
			defer mu.Unlock()
			return append([]sseEvent(nil), got...)
		}, func() {
			close(done)
			mgr.Unregister(client.ID())
		}
}
