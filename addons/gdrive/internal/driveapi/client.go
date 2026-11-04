// SPDX-License-Identifier: MIT

// Package driveapi is the part of the Google Drive v3 REST API that the
// Google Drive add-on uses. No address comes from a model, so the base address
// is fixed in the code. A package build or a test may replace it at link time
// with -ldflags "-X <package>.DefaultBase=<url>"; the host never can.
//
// The access token is passed to each call by the caller and is never stored.
// It leaves this package only in the Authorization header, and an error built
// here never holds it, the text of an answer or the name of a file.
package driveapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// DefaultBase is the address of the API. It is a variable only so that the
// link step of a test build can replace it.
var DefaultBase = "https://www.googleapis.com"

// MaxBody bounds one answer read in memory. A larger answer is an error, not a
// cut document.
const MaxBody = 16 << 20

// Waits, in seconds, for a limit or an outage that states no end.
const (
	defaultLimitWait  = 30
	defaultServerWait = 5
)

// Errors of a call. None of them holds the token, the text of the answer or
// the name of a file.
var (
	// ErrUnauthorized means the service refused the access token (401).
	ErrUnauthorized = errors.New("google drive refused the access token")
	// ErrForbidden means the person has no right for this (403).
	ErrForbidden = errors.New("google drive refused the request for this file")
	// ErrScope means the access token lacks a scope the call needs (403).
	ErrScope = errors.New("the access token lacks a scope this call needs")
	// ErrNotFound means the file does not exist or the token cannot see it (404).
	ErrNotFound = errors.New("not found or the connection cannot see it")
	// ErrInvalid means google drive could not process the request (other 4xx).
	ErrInvalid = errors.New("google drive refused the request")
	// ErrRange means the byte range starts after the end of the file (416).
	ErrRange = errors.New("the offset is past the end of the file")
	// ErrUnavailable means a failure on the way.
	ErrUnavailable = errors.New("google drive is not reachable now")
	// ErrUnreadable means a change was accepted but its answer could not be read.
	ErrUnreadable = errors.New("google drive accepted the change but its answer was not readable")
	// ErrTooLarge means an answer above the bound of the call.
	ErrTooLarge = errors.New("the answer of google drive is too large")
)

// ServerError is a 5xx answer. It is retryable; for a change it also means the
// outcome is not known.
type ServerError struct{ RetryAfter int }

func (e *ServerError) Error() string { return "google drive failed to answer" }

// Client calls the API. The zero value is not ready; use New.
type Client struct {
	// Base is the address of the API without a trailing slash.
	Base string
	// HTTP refuses redirects (pkg/httpclient does by default) and ends a call
	// after 30 seconds.
	HTTP *http.Client
	// Stream has no total timeout, for a body that is copied to the host.
	Stream *http.Client
	// Now is the clock; a test fixes it.
	Now func() time.Time
}

// New returns a client of the real API.
func New() *Client {
	return &Client{Base: DefaultBase, HTTP: httpclient.NewWithTimeout(30 * time.Second), Stream: httpclient.New(), Now: time.Now}
}

// Response is the part of an answer a caller needs besides the body.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Request describes one call.
type Request struct {
	Method string
	// Path starts with a slash, for example /drive/v3/files.
	Path   string
	Query  url.Values
	Header http.Header
	// Body is sent as it is, with ContentType.
	Body        []byte
	ContentType string
}

func (c *Client) build(ctx context.Context, token string, r Request) (*http.Request, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	u := strings.TrimRight(c.Base, "/") + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var rd io.Reader
	if r.Body != nil {
		rd = bytes.NewReader(r.Body)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, ErrInvalid
	}
	for k, v := range r.Header {
		req.Header[k] = v
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", r.ContentType)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "LocalAI-gdrive-addon")
	return req, nil
}

// Do sends the request and reads at most max bytes of the answer. A non-2xx
// answer is an error from the list above, or a *contract.ErrRateLimited, or a
// *ServerError.
func (c *Client) Do(ctx context.Context, token string, r Request, max int64) (Response, error) {
	req, err := c.build(ctx, token, r)
	if err != nil {
		return Response{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, c.transport(ctx)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return Response{}, c.transport(ctx)
	}
	if int64(len(body)) > max {
		return Response{}, ErrTooLarge
	}
	out := Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return out, nil
	}
	return out, c.failure(resp.StatusCode, resp.Header, body)
}

// Open sends the request and returns the body of a 2xx answer as a stream.
// The caller closes it.
func (c *Client) Open(ctx context.Context, token string, r Request) (io.ReadCloser, error) {
	req, err := c.build(ctx, token, r)
	if err != nil {
		return nil, err
	}
	resp, err := c.Stream.Do(req)
	if err != nil {
		return nil, c.transport(ctx)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Body, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return nil, c.failure(resp.StatusCode, resp.Header, body)
}

func (c *Client) transport(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// The transport error names the URL, never a header, but the class is what
	// a caller can act on.
	return ErrUnavailable
}

// GetJSON calls a path with GET and decodes the answer into v.
func (c *Client) GetJSON(ctx context.Context, token, path string, query url.Values, v any) error {
	r, err := c.Do(ctx, token, Request{Path: path, Query: query}, MaxBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return ErrUnavailable
	}
	return nil
}

// SendJSON calls a path with a method that changes something, with body
// encoded as JSON, and decodes the answer into v when v is not nil. A change
// is never repeated here: the caller decides.
func (c *Client) SendJSON(ctx context.Context, method, token, path string, query url.Values, body, v any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return ErrInvalid
	}
	return c.send(ctx, token, Request{Method: method, Path: path, Query: query, Body: raw, ContentType: "application/json"}, v)
}

// SendRaw is SendJSON for a body that is not JSON, such as the content of a
// file or a multipart upload.
func (c *Client) SendRaw(ctx context.Context, method, token, path string, query url.Values, contentType string, body []byte, v any) error {
	if body == nil {
		body = []byte{}
	}
	return c.send(ctx, token, Request{Method: method, Path: path, Query: query, Body: body, ContentType: contentType}, v)
}

func (c *Client) send(ctx context.Context, token string, r Request, v any) error {
	resp, err := c.Do(ctx, token, r, MaxBody)
	if err != nil {
		return err
	}
	if v != nil {
		if err := json.Unmarshal(resp.Body, v); err != nil {
			// The change went through; only the answer is unreadable.
			return ErrUnreadable
		}
	}
	return nil
}

// failure maps a non-2xx answer. Only the machine readable reasons of the
// answer are read; its message is never kept.
func (c *Client) failure(status int, h http.Header, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusTooManyRequests:
		return contract.RateLimitedAfter(retryAfter(h, defaultLimitWait))
	case http.StatusForbidden:
		reasons := reasonsOf(body)
		for _, r := range reasons {
			switch r {
			case "rateLimitExceeded", "userRateLimitExceeded", "sharingRateLimitExceeded", "RATE_LIMIT_EXCEEDED":
				return contract.RateLimitedAfter(retryAfter(h, defaultLimitWait))
			}
		}
		for _, r := range reasons {
			switch r {
			case "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "insufficientPermissions":
				return ErrScope
			}
		}
		return ErrForbidden
	case http.StatusNotFound, http.StatusGone:
		return ErrNotFound
	case http.StatusRequestedRangeNotSatisfiable:
		return ErrRange
	}
	if status >= 500 {
		return &ServerError{RetryAfter: retryAfter(h, defaultServerWait)}
	}
	return ErrInvalid
}

// reasonsOf returns the reason codes of an error answer.
func reasonsOf(body []byte) []string {
	var e struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return nil
	}
	var out []string
	for _, x := range e.Error.Errors {
		out = append(out, x.Reason)
	}
	for _, x := range e.Error.Details {
		out = append(out, x.Reason)
	}
	return out
}

// retryAfter reads the Retry-After header in seconds, or returns def.
func retryAfter(h http.Header, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil {
		return min(max(n, 1), contract.MaxRetryAfter)
	}
	return def
}

// Describe is a fixed sentence for an error of this package, for a refusal. It
// exists so no caller formats the error of a transport.
func Describe(err error) string {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return ErrUnauthorized.Error()
	case errors.Is(err, ErrScope):
		return ErrScope.Error()
	case errors.Is(err, ErrForbidden):
		return ErrForbidden.Error()
	case errors.Is(err, ErrNotFound):
		return ErrNotFound.Error()
	case errors.Is(err, ErrRange):
		return ErrRange.Error()
	case errors.Is(err, ErrInvalid):
		return ErrInvalid.Error()
	case errors.Is(err, ErrTooLarge):
		return ErrTooLarge.Error()
	}
	return fmt.Sprint(ErrUnavailable)
}
