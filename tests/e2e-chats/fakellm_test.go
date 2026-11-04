package e2echats_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// fakeLLM is a scriptable OpenAI-compatible upstream. A LocalAI model of the
// cloud-proxy backend points at it, so the real server, the real chats loop
// and the real HTTP stack run; only the language model is faked.

// fakeCall is one tool call the fake model makes.
type fakeCall struct {
	Name string
	Args string
}

// fakeStep is one model reply.
type fakeStep struct {
	Reasoning string        // sent as <think>..</think> before the text
	Text      string        // the answer text
	Calls     []fakeCall    // tool calls, sent after the text
	Pause     time.Duration // wait before the first byte
	Hang      bool          // never answer; ends when the client goes away
	Status    int           // an HTTP error instead of an answer
	Usage     int           // prompt tokens to report; zero reports a size from the prompt
}

// fakeReq is what the chats loop asked of the model.
type fakeReq struct {
	Model    string
	Messages []fakeMsg
	Tools    []string
	Stream   bool
	Raw      string
}

type fakeMsg struct {
	Role    string
	Content string
}

// Prompt is every message joined. The loop sends the whole conversation to the
// model as one flattened message, so a script looks for its markers in it.
func (r fakeReq) Prompt() string {
	var parts []string
	for _, m := range r.Messages {
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n")
}

// LastUser is the text of the newest user message.
func (r fakeReq) LastUser() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].Content
		}
	}
	return ""
}

// AllUser joins every user message, oldest first.
func (r fakeReq) AllUser() string {
	var parts []string
	for _, m := range r.Messages {
		if m.Role == "user" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// ToolResults are the contents of the tool messages after the newest user message.
func (r fakeReq) ToolResults() []string {
	var out []string
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			break
		}
		if r.Messages[i].Role == "tool" {
			out = append([]string{r.Messages[i].Content}, out...)
		}
	}
	return out
}

// HasTool says whether the model was offered a tool.
func (r fakeReq) HasTool(name string) bool {
	for _, t := range r.Tools {
		if t == name {
			return true
		}
	}
	return false
}

type fakeLLM struct {
	srv *httptest.Server

	mu      sync.Mutex
	script  func(fakeReq) fakeStep
	reqs    []fakeReq
	active  int
	maxLive int
}

func newFakeLLM() *fakeLLM {
	f := &fakeLLM{script: func(fakeReq) fakeStep { return fakeStep{Text: "ok"} }}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Handle replaces the script.
func (f *fakeLLM) Handle(fn func(fakeReq) fakeStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = fn
}

// Reset clears the recorded requests and restores the default script.
func (f *fakeLLM) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs, f.maxLive = nil, 0
	f.script = func(fakeReq) fakeStep { return fakeStep{Text: "ok"} }
}

// Requests returns a copy of the recorded requests.
func (f *fakeLLM) Requests() []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeReq(nil), f.reqs...)
}

// MaxConcurrent is the most requests that were open at once.
func (f *fakeLLM) MaxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxLive
}

func (f *fakeLLM) URL() string { return f.srv.URL }
func (f *fakeLLM) Close()      { f.srv.Close() }

func parseFakeReq(body []byte) fakeReq {
	var raw struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(body, &raw)
	out := fakeReq{Model: raw.Model, Stream: raw.Stream, Raw: string(body)}
	for _, m := range raw.Messages {
		text := ""
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			text = s
		} else {
			var parts []struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(m.Content, &parts) == nil {
				for _, p := range parts {
					text += p.Text
				}
			}
		}
		out.Messages = append(out.Messages, fakeMsg{Role: m.Role, Content: text})
	}
	for _, t := range raw.Tools {
		out.Tools = append(out.Tools, t.Function.Name)
	}
	return out
}

func (f *fakeLLM) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"fake"}]}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	req := parseFakeReq(body)
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	script := f.script
	f.active++
	if f.active > f.maxLive {
		f.maxLive = f.active
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	step := script(req)
	if step.Hang {
		<-r.Context().Done()
		return
	}
	if step.Pause > 0 {
		select {
		case <-time.After(step.Pause):
		case <-r.Context().Done():
			return
		}
	}
	if step.Status != 0 {
		http.Error(w, `{"error":{"message":"fake upstream failure"}}`, step.Status)
		return
	}
	content := step.Text
	if step.Reasoning != "" {
		content = "<think>" + step.Reasoning + "</think>" + content
	}
	calls := make([]map[string]any, 0, len(step.Calls))
	for i, c := range step.Calls {
		calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i), "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": c.Args}})
	}
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	if !req.Stream {
		msg := map[string]any{"role": "assistant", "content": content}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-fake", "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
			"usage":   map[string]int{"prompt_tokens": len(req.Raw) / 4, "completion_tokens": 5, "total_tokens": len(req.Raw)/4 + 5}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	send := func(delta map[string]any, fin any) {
		b, _ := json.Marshal(map[string]any{"id": "chatcmpl-fake", "object": "chat.completion.chunk", "model": req.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": fin}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	send(map[string]any{"role": "assistant", "content": ""}, nil)
	for _, piece := range chunkText(content, 12) {
		send(map[string]any{"content": piece}, nil)
	}
	for _, c := range calls {
		send(map[string]any{"tool_calls": []map[string]any{c}}, nil)
	}
	send(map[string]any{}, finish)
	// Token usage is how the loop learns that the window is filling.
	prompt := step.Usage
	if prompt == 0 {
		prompt = len(req.Raw) / 4 // the whole request, tool schemas included, like a real provider counts
	}
	ub, _ := json.Marshal(map[string]any{"id": "chatcmpl-fake", "object": "chat.completion.chunk", "model": req.Model, "choices": []any{},
		"usage": map[string]int{"prompt_tokens": prompt, "completion_tokens": len(content) / 4, "total_tokens": prompt + len(content)/4}})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", ub)
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

func chunkText(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// CountWith is the number of recorded requests whose prompt holds the marker.
// The request being answered is already recorded, so the first one counts 1.
func (f *fakeLLM) CountWith(marker string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if strings.Contains(r.Prompt(), marker) {
			n++
		}
	}
	return n
}

// Open is the number of requests that are open now.
func (f *fakeLLM) Open() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}
