// SPDX-License-Identifier: MIT

// Package googletest holds what the fake Google APIs of the add-on tests have
// in common: the tokens and their scopes, the record of the calls, injected
// failures, and writes that are done but never answered. A fake embeds Core
// and adds its own data and routes.
package googletest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Principal is who a token stands for.
type Principal struct {
	Email  string
	Scopes map[string]bool
}

// Call is one request the fake received.
type Call struct {
	Method, Path, Query, Token, Range, ContentType string
	// Body is the start of the body, up to 64 KiB.
	Body string
}

type injected struct {
	status     int
	reason     string
	retryAfter string
	times      int // 0: always
	pathPrefix string
	method     string // empty: any
}

// Core is the shared part of a fake. Its lock guards the fake's own data too:
// a route runs with it held.
type Core struct {
	// Mu guards every field of Core and of the fake that embeds it.
	Mu sync.Mutex
	// IsWrite says whether a method is a change, for DropWrites.
	IsWrite func(method string) bool

	tokens     map[string]*Principal
	auth       func(token string) (email string, scopes []string, ok bool)
	calls      []Call
	inject     []*injected
	dropWrites bool
}

// Init prepares the Core. isWrite lists the methods DropWrites applies to.
func (c *Core) Init(isWrite func(method string) bool) {
	c.IsWrite = isWrite
	c.ResetCore()
}

// ResetCore forgets the tokens, the calls, the injected failures and the
// authenticator. The caller holds Mu.
func (c *Core) ResetCore() {
	c.tokens, c.auth, c.calls, c.inject, c.dropWrites = map[string]*Principal{}, nil, nil, nil, false
}

// DropWrites makes the fake do a change and then close the connection
// without an answer, as a network that fails after the request left.
func (c *Core) DropWrites(on bool) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.dropWrites = on
}

// SetAuthenticator accepts the tokens f accepts, besides the ones added.
func (c *Core) SetAuthenticator(f func(token string) (email string, scopes []string, ok bool)) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.auth = f
}

// AddToken makes token valid for email with scopes.
func (c *Core) AddToken(token, email string, scopes ...string) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	p := &Principal{Email: email, Scopes: map[string]bool{}}
	for _, sc := range scopes {
		p.Scopes[sc] = true
	}
	c.tokens[token] = p
}

// RevokeToken makes token invalid.
func (c *Core) RevokeToken(token string) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	delete(c.tokens, token)
}

// Calls returns the requests received so far.
func (c *Core) Calls() []Call {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	return append([]Call(nil), c.calls...)
}

// Inject makes the next times calls under pathPrefix ("" for any) answer with
// status and the error reason. retryAfter, when not empty, is sent in the
// Retry-After header. times 0 means every call.
func (c *Core) Inject(pathPrefix string, status int, reason, retryAfter string, times int) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.inject = append(c.inject, &injected{status: status, reason: reason, retryAfter: retryAfter, times: times, pathPrefix: pathPrefix})
}

// InjectMethod is Inject for the calls with one HTTP method only, so a test
// can refuse a change and let the reads before it through.
func (c *Core) InjectMethod(method, pathPrefix string, status int, reason string, times int) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.inject = append(c.inject, &injected{status: status, reason: reason, times: times, pathPrefix: pathPrefix, method: method})
}

// ClearInjected removes every injected answer.
func (c *Core) ClearInjected() {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.inject = nil
}

// Reply writes v as JSON with status.
func Reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Fail writes an error answer in the shape of the Google APIs.
func Fail(w http.ResponseWriter, status int, reason, message string) {
	Reply(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": message,
		"errors": []map[string]string{{"reason": reason, "message": message}},
	}})
}

func (c *Core) record(r *http.Request, token string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if len(body) > 64<<10 {
		body = body[:64<<10]
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	c.calls = append(c.calls, Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Token: token, Range: r.Header.Get("Range"),
		ContentType: r.Header.Get("Content-Type"), Body: string(body)})
}

// Serve records the request, applies an injected failure, finds the
// principal of the token and calls route. With DropWrites on, a change is
// routed into a recorder and the connection is closed. It holds Mu.
func (c *Core) Serve(w http.ResponseWriter, r *http.Request, route func(w http.ResponseWriter, r *http.Request, p *Principal)) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c.record(r, token)
	for i, in := range c.inject {
		if !strings.HasPrefix(r.URL.Path, in.pathPrefix) || (in.method != "" && in.method != r.Method) {
			continue
		}
		if in.times > 0 {
			in.times--
			if in.times == 0 {
				c.inject = append(c.inject[:i], c.inject[i+1:]...)
			}
		}
		if in.retryAfter != "" {
			w.Header().Set("Retry-After", in.retryAfter)
		}
		Fail(w, in.status, in.reason, "injected")
		return
	}
	p, ok := c.tokens[token]
	if !ok && c.auth != nil {
		if email, scopes, valid := c.auth(token); valid {
			p, ok = &Principal{Email: email, Scopes: map[string]bool{}}, true
			for _, sc := range scopes {
				p.Scopes[sc] = true
			}
		}
	}
	if !ok {
		Fail(w, http.StatusUnauthorized, "authError", "Invalid Credentials")
		return
	}
	if c.dropWrites && c.IsWrite(r.Method) {
		route(httptest.NewRecorder(), r, p)
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	route(w, r, p)
}
