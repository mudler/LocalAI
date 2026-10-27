package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"

	"github.com/mudler/LocalAI/core/http/auth"
)

// maxRewriteBody caps a request body that LLMWith reads to add its fields. A
// request with attached images can be large; nothing legitimate is bigger.
const maxRewriteBody = 512 << 20

// usageTail is how much of a response is kept to find its usage object. The
// object comes last, in the final chunk of a stream or at the end of the
// JSON body.
const usageTail = 16 << 10

// Params are the sampling fields and the usage hook of one chat's model
// client. A nil pointer or an empty string leaves the model's own default.
type Params struct {
	Temperature     *float64
	TopP            *float64
	TopK            *int
	ReasoningEffort string
	// OnUsage, when set, is called once for each model request that reports
	// token counts. It asks the route for stream usage and timings.
	OnUsage func(Usage)
}

// Usage is what one model request used.
type Usage struct {
	// PromptTokens is the size of the context the request sent: the number the
	// context meter shows.
	PromptTokens     int
	CompletionTokens int
	// TokensPerSecond is the generation speed, or 0 when the backend gave no timing.
	TokensPerSecond float64
}

// LLMWith is LLM with the chat's own settings added to every request.
func (p *Provider) LLMWith(user *auth.User, model string, params Params) cogito.LLM {
	llm := clients.NewLocalAILLM(model, "", baseURL)
	client := p.transport.Client(user)
	if params.isZero() {
		llm.SetHTTPClient(client)
		return llm
	}
	client.Transport = &paramsTransport{next: client.Transport, params: params}
	llm.SetHTTPClient(client)
	return llm
}

func (p Params) isZero() bool {
	return p.Temperature == nil && p.TopP == nil && p.TopK == nil && p.ReasoningEffort == "" && p.OnUsage == nil
}

type paramsTransport struct {
	next   http.RoundTripper
	params Params
}

func (t *paramsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/chat/completions") || req.Body == nil {
		return t.next.RoundTrip(req)
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxRewriteBody+1))
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("studio provider: read the request: %w", err)
	}
	if len(raw) > maxRewriteBody {
		return nil, fmt.Errorf("studio provider: the request is larger than %d bytes", maxRewriteBody)
	}
	body, err := t.rewrite(raw)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	if t.params.OnUsage != nil {
		// Without this header the route leaves the timings out of the usage.
		req.Header.Set("Extra-Usage", "1")
	}
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	if err != nil || t.params.OnUsage == nil || resp.StatusCode/100 != 2 {
		return resp, err
	}
	resp.Body = &usageBody{rc: resp.Body, start: start, report: t.params.OnUsage}
	return resp, nil
}

// rewrite adds the chat's settings to a chat request. Fields the chat sets win
// over what the loop wrote, because the person chose them.
func (t *paramsTransport) rewrite(raw []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("studio provider: the request is not JSON: %w", err)
	}
	set := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		m[key] = b
		return nil
	}
	var err error
	if t.params.Temperature != nil {
		err = set("temperature", *t.params.Temperature)
	}
	if err == nil && t.params.TopP != nil {
		err = set("top_p", *t.params.TopP)
	}
	if err == nil && t.params.TopK != nil {
		err = set("top_k", *t.params.TopK)
	}
	if err == nil && t.params.ReasoningEffort != "" {
		err = set("reasoning_effort", t.params.ReasoningEffort)
	}
	if err == nil && t.params.OnUsage != nil && string(m["stream"]) == "true" {
		err = set("stream_options", map[string]bool{"include_usage": true})
	}
	if err != nil {
		return nil, fmt.Errorf("studio provider: set the chat settings: %w", err)
	}
	return json.Marshal(m)
}

// usageBody passes a response through and reports the usage it carries when
// the body ends.
type usageBody struct {
	rc     io.ReadCloser
	start  time.Time
	report func(Usage)

	mu   sync.Mutex
	tail []byte
	done bool
}

func (b *usageBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.mu.Lock()
		b.tail = append(b.tail, p[:n]...)
		if len(b.tail) > usageTail {
			b.tail = append(b.tail[:0], b.tail[len(b.tail)-usageTail:]...)
		}
		b.mu.Unlock()
	}
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *usageBody) Close() error {
	b.finish()
	return b.rc.Close()
}

func (b *usageBody) finish() {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return
	}
	b.done = true
	tail := b.tail
	b.mu.Unlock()
	if u, ok := parseUsage(tail, time.Since(b.start)); ok {
		b.report(u)
	}
}

// parseUsage finds the last "usage" object in the tail of a response.
func parseUsage(tail []byte, elapsed time.Duration) (Usage, bool) {
	i := bytes.LastIndex(tail, []byte(`"usage":`))
	if i < 0 {
		return Usage{}, false
	}
	var u struct {
		Prompt     int     `json:"prompt_tokens"`
		Completion int     `json:"completion_tokens"`
		GenMillis  float64 `json:"timing_token_generation"`
	}
	dec := json.NewDecoder(bytes.NewReader(tail[i+len(`"usage":`):]))
	if err := dec.Decode(&u); err != nil || (u.Prompt == 0 && u.Completion == 0) {
		return Usage{}, false
	}
	out := Usage{PromptTokens: u.Prompt, CompletionTokens: u.Completion}
	switch {
	case u.GenMillis > 0:
		out.TokensPerSecond = float64(u.Completion) / (u.GenMillis / 1000)
	case elapsed > 0 && u.Completion > 0:
		// The backend gave no timing: the whole request time is a lower bound on speed.
		out.TokensPerSecond = float64(u.Completion) / elapsed.Seconds()
	}
	return out, true
}
