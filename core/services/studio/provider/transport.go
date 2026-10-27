// Package provider gives the studio loop its model client. The loop reaches the
// model through the application's own chat route, in memory and as the user, so
// the route's whole chain (feature gate, model allowlist, quota, router, PII
// redaction, admission control, usage log) applies and nothing is re-implemented.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mudler/cogito"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/model"
)

// Transport serves HTTP requests by calling a handler in the same process. No
// socket is opened. It is an http.RoundTripper; Client wraps it for one user.
type Transport struct {
	mu sync.RWMutex
	h  http.Handler
}

// NewTransport returns a Transport with no handler. Requests fail until
// SetHandler is called, which the HTTP server does once its router exists.
func NewTransport() *Transport { return &Transport{} }

// SetHandler sets the handler that serves the requests, normally the
// application's router.
func (t *Transport) SetHandler(h http.Handler) {
	t.mu.Lock()
	t.h = h
	t.mu.Unlock()
}

func (t *Transport) handler() http.Handler {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.h
}

// Client returns an http.Client that sends its requests through t as user. A
// nil user sends them with no identity, which works when auth is off.
func (t *Transport) Client(user *auth.User) *http.Client {
	return &http.Client{Transport: userTransport{t: t, user: user, fails: new(atomic.Int32)}}
}

// MaxModelFailures is how many requests of one client may fail in a row before
// the client gives up for good. The loop above it retries a failed request with
// a growing wait, so without a bound a model that is down keeps a turn "running"
// for minutes and tells the user nothing.
const MaxModelFailures = 4

type userTransport struct {
	t    *Transport
	user *auth.User
	// fails counts the failed requests since the last success. One client
	// serves one turn.
	fails *atomic.Int32
}

func (u userTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if u.fails.Load() >= MaxModelFailures {
		return nil, u.giveUp("")
	}
	if u.user != nil {
		req = req.Clone(auth.WithInternalUser(req.Context(), u.user))
	}
	req = dedupeTools(req)
	resp, err := u.t.RoundTrip(req)
	if err != nil {
		if req.Context().Err() != nil {
			return resp, err // the caller stopped; the model did not fail
		}
		return resp, u.failed(err.Error())
	}
	// A refusal ends the turn with a reason the user can act on. The marker
	// is how the loop recognizes it through the model client's own wrapping.
	if resp.Header.Get(model.LoadFailedHeader) != "" {
		// The model's backend failed to start. Waiting does not fix a missing
		// backend, and each retry only meets the same refusal, so the turn
		// ends and tells the user why.
		reason := loadFailureReason(resp)
		_ = resp.Body.Close()
		return nil, cogito.Permanent(errors.New("the model could not be loaded: " + reason))
	}
	switch resp.StatusCode {
	case http.StatusForbidden:
		_ = resp.Body.Close()
		return nil, cogito.Permanent(errors.New("the model refused the request " + session.MarkNotAllowed))
	case http.StatusTooManyRequests:
		_ = resp.Body.Close()
		return nil, cogito.Permanent(errors.New("the model refused the request " + session.MarkQuota))
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		// A server error may pass: the loop above retries it. The last one
		// allowed is turned into a permanent error.
		if u.fails.Add(1) >= MaxModelFailures {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
			_ = resp.Body.Close()
			return nil, u.giveUp(fmt.Sprintf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(snippet))))
		}
		return resp, nil
	}
	u.fails.Store(0)
	return resp, nil
}

// failed counts a request that did not get an answer. It returns an ordinary
// error while retries remain and a permanent one after the last.
func (u userTransport) failed(why string) error {
	if u.fails.Add(1) >= MaxModelFailures {
		return u.giveUp(why)
	}
	return errors.New("the model request failed: " + why)
}

func (u userTransport) giveUp(why string) error {
	msg := fmt.Sprintf("the model failed %d times in a row", MaxModelFailures)
	if why != "" {
		msg += " (last: " + why + ")"
	}
	return cogito.Permanent(errors.New(msg))
}

// dedupeTools drops a tool that the request names a second time. Providers such
// as OpenAI and Anthropic refuse a request with two tools of one name, and the
// loop above assembles the list from several sources. The first one wins. A
// request with no repeated tool is passed on as it is.
func dedupeTools(req *http.Request) *http.Request {
	if req.Method != http.MethodPost || req.Body == nil {
		return req
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return req
	}
	out := raw
	if fixed, changed := dedupeToolsJSON(raw); changed {
		out = fixed
	}
	req = req.Clone(req.Context())
	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	return req
}

func dedupeToolsJSON(raw []byte) ([]byte, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil || doc["tools"] == nil {
		return raw, false
	}
	var tools []json.RawMessage
	if json.Unmarshal(doc["tools"], &tools) != nil {
		return raw, false
	}
	seen := make(map[string]bool, len(tools))
	kept := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		var named struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if json.Unmarshal(t, &named) == nil && named.Function.Name != "" {
			if seen[named.Function.Name] {
				continue
			}
			seen[named.Function.Name] = true
		}
		kept = append(kept, t)
	}
	if len(kept) == len(tools) {
		return raw, false
	}
	doc["tools"], _ = json.Marshal(kept)
	fixed, err := json.Marshal(doc)
	if err != nil {
		return raw, false
	}
	return fixed, true
}

// loadFailureReason reads the message of a refusal caused by a failed model load.
func loadFailureReason(resp *http.Response) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return "its backend failed to start; check the model's configuration"
}

// RoundTrip runs the handler in a goroutine and returns as soon as it has
// written its status, with a body that streams what it writes next. Closing the
// body, or cancelling the request's context, cancels the handler's context.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	h := t.handler()
	if h == nil {
		return nil, errors.New("studio provider: no handler is set yet")
	}
	ctx, cancel := context.WithCancel(req.Context())
	req = req.Clone(ctx)
	req.RequestURI = req.URL.RequestURI()
	req.Host = req.URL.Host

	pr, pw := io.Pipe()
	w := &pipeWriter{header: http.Header{}, ready: make(chan struct{}), pw: pw}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pw.CloseWithError(fmt.Errorf("studio provider: the handler panicked: %v", r))
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK) // a handler that wrote nothing still answered
			_ = pw.Close()
		}()
		h.ServeHTTP(w, req)
	}()

	select {
	case <-w.ready:
	case <-ctx.Done():
		cancel()
		_ = pr.CloseWithError(ctx.Err())
		return nil, ctx.Err()
	}
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", w.status, http.StatusText(w.status)),
		StatusCode: w.status,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  w.snapshot,
		Body:    &body{r: pr, cancel: cancel},
		Request: req,
	}, nil
}

// body is the response body. Closing it tells the handler the caller is gone.
type body struct {
	r      *io.PipeReader
	cancel context.CancelFunc
}

func (b *body) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *body) Close() error {
	b.cancel()
	return b.r.Close()
}

// pipeWriter is the http.ResponseWriter the handler sees. The first Write,
// Flush or WriteHeader releases the response; later writes go down the pipe.
type pipeWriter struct {
	header   http.Header
	snapshot http.Header
	status   int
	once     sync.Once
	ready    chan struct{}
	pw       *io.PipeWriter
}

func (w *pipeWriter) Header() http.Header { return w.header }

func (w *pipeWriter) WriteHeader(code int) {
	w.once.Do(func() {
		w.status = code
		w.snapshot = w.header.Clone()
		close(w.ready)
	})
}

func (w *pipeWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

// Flush releases the response head. The pipe itself has no buffer, so what was
// written has already reached the reader.
func (w *pipeWriter) Flush() { w.WriteHeader(http.StatusOK) }
