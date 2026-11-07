// SPDX-License-Identifier: MIT

// Package googleapi is the HTTP client the Google add-ons share. A service
// package, such as driveapi or gmailapi, fixes the base address and the paths
// and keeps what only its API needs.
//
// The access token is passed to each call by the caller and is never stored.
// It leaves this package only in the Authorization header, and an error built
// here never holds it or the text of an answer.
package googleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// MaxBody bounds one answer read in memory. A larger answer is an error, not a
// cut document.
const MaxBody = 16 << 20

// Waits, in seconds, for a limit or an outage that states no end.
const (
	defaultLimitWait  = 30
	defaultServerWait = 5
)

// Errors of a call. None of them holds the token or the text of the answer.
var (
	// ErrUnauthorized means the service refused the access token (401).
	ErrUnauthorized = errors.New("the service refused the access token")
	// ErrForbidden means the person has no right for this (403).
	ErrForbidden = errors.New("the service refused the request")
	// ErrScope means the access token lacks a scope the call needs (403).
	ErrScope = errors.New("the access token lacks a scope this call needs")
	// ErrNotFound means the object does not exist or the token cannot see it (404).
	ErrNotFound = errors.New("not found or the connection cannot see it")
	// ErrInvalid means the service could not process the request (other 4xx).
	ErrInvalid = errors.New("the service refused the request")
	// ErrUnavailable means a failure on the way.
	ErrUnavailable = errors.New("the service is not reachable now")
	// ErrUnreadable means a change was accepted but its answer could not be read.
	ErrUnreadable = errors.New("the service accepted the change but its answer was not readable")
	// ErrTooLarge means an answer above the bound of the call.
	ErrTooLarge = errors.New("the answer of the service is too large")
	// ErrDecode means a successful answer that is not the JSON the call expects.
	ErrDecode = errors.New("the answer of the service was not readable")
)

// ServerError is a 5xx answer. It is retryable; for a change it also means the
// outcome is not known.
type ServerError struct{ RetryAfter int }

func (e *ServerError) Error() string { return "the service failed to answer" }

// Client calls a Google API. The zero value is not ready; use New.
type Client struct {
	// Base is the address of the API without a trailing slash.
	Base string
	// UserAgent is sent with each call.
	UserAgent string
	// HTTP refuses redirects (pkg/httpclient does by default) and ends a call
	// after 30 seconds.
	HTTP *http.Client
	// Stream has no total timeout, for a body that is copied to the host.
	Stream *http.Client
	// StatusErrors maps a status that only this service gives to its error.
	// It is read before the general rules of Failure.
	StatusErrors map[int]error
	// Now is the clock; a test fixes it.
	Now func() time.Time
}

// New returns a client of base.
func New(base, userAgent string) *Client {
	return &Client{
		Base:      base,
		UserAgent: userAgent,
		HTTP:      httpclient.NewWithTimeout(30 * time.Second),
		Stream:    httpclient.New(),
		Now:       time.Now,
	}
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

// NewRequest builds the HTTP request of r.
func (c *Client) NewRequest(ctx context.Context, token string, r Request) (*http.Request, error) {
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
	req.Header.Set("User-Agent", c.UserAgent)
	return req, nil
}

// Do sends the request and reads at most limit bytes of the answer. A non-2xx
// answer is an error from the list above, or a *contract.ErrRateLimited, or a
// *ServerError.
func (c *Client) Do(ctx context.Context, token string, r Request, limit int64) (Response, error) {
	req, err := c.NewRequest(ctx, token, r)
	if err != nil {
		return Response{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, c.Transport(ctx)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Response{}, c.Transport(ctx)
	}
	if int64(len(body)) > limit {
		return Response{}, ErrTooLarge
	}
	out := Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	if ok(resp.StatusCode) {
		return out, nil
	}
	return out, c.Failure(resp.StatusCode, resp.Header, body)
}

func ok(status int) bool { return status >= 200 && status < 300 }

// Transport maps a failure on the way. The transport error names the URL,
// never a header, but the class is what a caller can act on.
func (c *Client) Transport(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}

// GetJSON calls a path with GET and decodes the answer into v.
func (c *Client) GetJSON(ctx context.Context, token, path string, query url.Values, v any) error {
	r, err := c.Do(ctx, token, Request{Path: path, Query: query}, MaxBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return ErrDecode
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
	return c.Send(ctx, token, Request{Method: method, Path: path, Query: query, Body: raw, ContentType: "application/json"}, v)
}

// Send is Do for a change: it decodes the answer into v when v is not nil.
func (c *Client) Send(ctx context.Context, token string, r Request, v any) error {
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

// Failure maps a non-2xx answer. Only the machine readable reasons of the
// answer are read; its message is never kept.
func (c *Client) Failure(status int, h http.Header, body []byte) error {
	if err, ok := c.StatusErrors[status]; ok {
		return err
	}
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
	}
	if status >= 500 {
		return &ServerError{RetryAfter: retryAfter(h, defaultServerWait)}
	}
	return ErrInvalid
}

func retryAfter(h http.Header, def int) int {
	return contract.ParseRetryAfter(h.Get("Retry-After"), def)
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

// Texts overrides the sentence Describe gives for an error, for a service
// that words it for its own objects.
type Texts map[error]string

// Describe is a fixed sentence for an error of this package, for a refusal. It
// exists so no caller formats the error of a transport. An error that is not
// a refusal of the service reads as ErrUnavailable. texts words the sentences
// for the objects of the service and may add errors of its own.
func Describe(err error, texts Texts) string {
	for _, e := range []error{ErrUnauthorized, ErrScope, ErrForbidden, ErrNotFound, ErrInvalid, ErrTooLarge} {
		if errors.Is(err, e) {
			return texts.of(e)
		}
	}
	// An error only this service gives, such as a range it refuses.
	for e, s := range texts {
		if errors.Is(err, e) {
			return s
		}
	}
	return texts.of(ErrUnavailable)
}

func (t Texts) of(err error) string {
	if s, ok := t[err]; ok {
		return s
	}
	return err.Error()
}
