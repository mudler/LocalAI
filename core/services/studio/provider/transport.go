// Package provider gives the studio loop its model client. The loop reaches the
// model through the application's own chat route, in memory and as the user, so
// the route's whole chain (feature gate, model allowlist, quota, router, PII
// redaction, admission control, usage log) applies and nothing is re-implemented.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/mudler/LocalAI/core/http/auth"
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
	return &http.Client{Transport: userTransport{t: t, user: user}}
}

type userTransport struct {
	t    *Transport
	user *auth.User
}

func (u userTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if u.user != nil {
		req = req.Clone(auth.WithInternalUser(req.Context(), u.user))
	}
	return u.t.RoundTrip(req)
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
