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
	Model      string
	Stream     bool
	Messages   []map[string]any
	Tools      []string
	ToolChoice any
}

// fakeLLM is an OpenAI-compatible chat endpoint. The standalone pool reaches its
// LLM over HTTP (apiURL), so a real server is the only seam that exercises the
// whole agent loop without a model.
type fakeLLM struct {
	srv      *httptest.Server
	mu       sync.Mutex
	reply    string
	requests []fakeLLMRequest
	toolName string
	toolArgs string
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

// SetToolCall makes the fake answer with one call to the named function until
// the conversation carries a tool result, then with the plain reply. Keying on
// the tool message rather than a request counter keeps the fake independent of
// how many planning requests the agent makes before it runs the tool. The
// flip side: tool mode stays on until a request carries a role "tool"
// message, so a client that restarts with trimmed history would get the
// tool call again and loop until its iteration cap.
func (f *fakeLLM) SetToolCall(name, argsJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolName = name
	f.toolArgs = argsJSON
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
		Tools    []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice any `json:"tool_choice"`
	}
	_ = json.Unmarshal(body, &req)

	var tools []string
	for _, t := range req.Tools {
		tools = append(tools, t.Function.Name)
	}
	hasToolResult := false
	for _, m := range req.Messages {
		if m["role"] == "tool" {
			hasToolResult = true
		}
	}

	f.mu.Lock()
	f.requests = append(f.requests, fakeLLMRequest{Model: req.Model, Stream: req.Stream, Messages: req.Messages, Tools: tools, ToolChoice: req.ToolChoice})
	reply := f.reply
	var toolCall map[string]any
	if f.toolName != "" && !hasToolResult {
		toolCall = map[string]any{
			"index": 0, "id": "call_fake", "type": "function",
			"function": map[string]any{"name": f.toolName, "arguments": f.toolArgs},
		}
	}
	f.mu.Unlock()

	message := map[string]any{"role": "assistant", "content": reply}
	finish := "stop"
	if toolCall != nil {
		// "index" belongs only to streaming deltas, so the non-streaming
		// message carries a copy without it.
		plain := map[string]any{}
		for k, v := range toolCall {
			if k != "index" {
				plain[k] = v
			}
		}
		message = map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{plain}}
		finish = "tool_calls"
	}

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-fake", "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       message,
				"finish_reason": finish,
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
	if toolCall != nil {
		chunk(map[string]any{"role": "assistant", "tool_calls": []map[string]any{toolCall}}, nil)
	} else {
		chunk(map[string]any{"role": "assistant", "content": reply}, nil)
	}
	chunk(map[string]any{}, finish)
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
// expired without calling the LLM or recording an observable. This depends on
// LocalAGI not short-circuiting a cancelled job before a worker receives it,
// so re-check it when LocalAGI is bumped; the timeout turns a hang into a
// failure if that ever changes.
func awaitRunning(svc *agentpool.AgentPoolService, userID, name string) {
	a := svc.GetAgentForUser(userID, name)
	Expect(a).ToNot(BeNil())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Execute(types.NewJob(types.WithContext(ctx)))
	}()
	Eventually(done, "10s").Should(BeClosed())
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
	// Include the user: the manager keys listeners by ID, so two users'
	// same-named agents must never share one if a manager is ever shared.
	client := sse.NewClient("contract-" + userID + "-" + name)
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
