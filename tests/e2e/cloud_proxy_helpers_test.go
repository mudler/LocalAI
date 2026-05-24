package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

// upstreamRecorder captures whatever request the cloud-proxy backend
// forwarded to the fake upstream. Tests assert against the captured
// fields to prove the body / headers / model rewrite landed correctly.
type upstreamRecorder struct {
	mu          sync.Mutex
	Method      string
	Path        string
	Header      http.Header
	Body        []byte
	RequestHits int32
}

func (r *upstreamRecorder) Hits() int {
	return int(atomic.LoadInt32(&r.RequestHits))
}

func (r *upstreamRecorder) snapshot() (method, path string, hdr http.Header, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Clone header so the test can read after the next request lands.
	cloned := http.Header{}
	for k, v := range r.Header {
		cloned[k] = append([]string{}, v...)
	}
	return r.Method, r.Path, cloned, append([]byte(nil), r.Body...)
}

// fakeOpenAIUpstreamServer stands up an httptest server that mimics
// OpenAI Chat Completions. The script chooses what to return per
// request — tests with different cases swap script via SetScript.
type fakeOpenAIUpstreamServer struct {
	srv      *httptest.Server
	recorder upstreamRecorder

	mu     sync.Mutex
	script func(req []byte) (status int, body string, contentType string)
}

func newFakeOpenAIUpstream() *fakeOpenAIUpstreamServer {
	f := &fakeOpenAIUpstreamServer{}
	f.SetScript(func([]byte) (int, string, string) {
		// Default: a trivial non-streaming text reply, no tool calls.
		return 200, `{"id":"chatcmpl-x","choices":[{"index":0,"message":{"role":"assistant","content":"hello from fake openai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`, "application/json"
	})
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeOpenAIUpstreamServer) serve(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&f.recorder.RequestHits, 1)
	body, _ := io.ReadAll(r.Body)
	f.recorder.mu.Lock()
	f.recorder.Method = r.Method
	f.recorder.Path = r.URL.Path
	f.recorder.Header = r.Header.Clone()
	f.recorder.Body = body
	f.recorder.mu.Unlock()

	f.mu.Lock()
	script := f.script
	f.mu.Unlock()
	status, replyBody, contentType := script(body)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, replyBody)
}

func (f *fakeOpenAIUpstreamServer) URL() string { return f.srv.URL }
func (f *fakeOpenAIUpstreamServer) Close()      { f.srv.Close() }

func (f *fakeOpenAIUpstreamServer) SetScript(script func(req []byte) (status int, body string, contentType string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = script
}

// Snapshot returns the most-recently captured request data.
func (f *fakeOpenAIUpstreamServer) Snapshot() (method, path string, hdr http.Header, body []byte) {
	return f.recorder.snapshot()
}

// DecodedBody returns the captured body parsed as a generic OpenAI
// request. Helper for tests that want to assert specific fields
// (e.g. model rewrite, stream flag) without re-parsing inline.
func (f *fakeOpenAIUpstreamServer) DecodedBody() map[string]any {
	_, _, _, body := f.Snapshot()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return m
}

// fakeAnthropicUpstreamServer is the Anthropic counterpart.
type fakeAnthropicUpstreamServer struct {
	srv      *httptest.Server
	recorder upstreamRecorder

	mu     sync.Mutex
	script func(req []byte) (status int, body string, contentType string)
}

func newFakeAnthropicUpstream() *fakeAnthropicUpstreamServer {
	f := &fakeAnthropicUpstreamServer{}
	f.SetScript(func([]byte) (int, string, string) {
		return 200, `{"id":"msg_x","type":"message","role":"assistant","content":[{"type":"text","text":"hello from fake anthropic"}],"model":"claude-fake","usage":{"input_tokens":3,"output_tokens":5}}`, "application/json"
	})
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeAnthropicUpstreamServer) serve(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&f.recorder.RequestHits, 1)
	body, _ := io.ReadAll(r.Body)
	f.recorder.mu.Lock()
	f.recorder.Method = r.Method
	f.recorder.Path = r.URL.Path
	f.recorder.Header = r.Header.Clone()
	f.recorder.Body = body
	f.recorder.mu.Unlock()

	f.mu.Lock()
	script := f.script
	f.mu.Unlock()
	status, replyBody, contentType := script(body)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, replyBody)
}

func (f *fakeAnthropicUpstreamServer) URL() string { return f.srv.URL }
func (f *fakeAnthropicUpstreamServer) Close()      { f.srv.Close() }

func (f *fakeAnthropicUpstreamServer) SetScript(script func(req []byte) (status int, body string, contentType string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = script
}

func (f *fakeAnthropicUpstreamServer) Snapshot() (method, path string, hdr http.Header, body []byte) {
	return f.recorder.snapshot()
}

func (f *fakeAnthropicUpstreamServer) DecodedBody() map[string]any {
	_, _, _, body := f.Snapshot()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return m
}

// streamingOpenAIToolCallScript returns an SSE response that announces
// a single tool call broken across delta fragments. The wire shape
// matches what OpenAI actually emits; used to verify cloud-proxy
// translate-mode preserves tool calls through HTTP.
func streamingOpenAIToolCallScript() (status int, body string, contentType string) {
	frames := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_e2e","type":"function","function":{"name":"get_weather"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":\"SF\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return 200, b.String(), "text/event-stream"
}

// nonStreamingOpenAIToolCallScript returns a non-streaming tool-call
// response with id/name/arguments fully populated.
func nonStreamingOpenAIToolCallScript() (status int, body string, contentType string) {
	return 200, `{"id":"chatcmpl-y","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"clouds\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`, "application/json"
}

// emailLeakOpenAIScript returns a non-streaming response containing an
// email address. The streaming PII filter doesn't apply to buffered
// responses, but the response is JSON the client receives unchanged —
// used to verify the wire path without PII assertions. The streaming
// PII variant uses an SSE response.
func emailLeakOpenAIStreamingScript() (status int, body string, contentType string) {
	frames := []string{
		`{"choices":[{"index":0,"delta":{"content":"contact alice@"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"example.com please"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return 200, b.String(), "text/event-stream"
}
