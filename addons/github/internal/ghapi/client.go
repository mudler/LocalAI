// SPDX-License-Identifier: MIT

// Package ghapi is the part of the GitHub REST API that the GitHub add-on
// reads. It speaks to api.github.com only: no address comes from a model, so
// the base address is fixed in the code and only a test replaces it.
//
// The token is passed to each call by the caller and is never stored. It
// leaves this package only in the Authorization header, and an error built
// here never holds it.
package ghapi

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

// DefaultBase is the address of the API.
const DefaultBase = "https://api.github.com"

// APIVersion is the version of the REST API the add-on asks for.
const APIVersion = "2022-11-28"

// MaxBody bounds one answer read in memory. A larger answer is an error, not a
// cut document.
const MaxBody = 16 << 20

// defaultWait is how long a limit with no stated end lasts, in seconds. GitHub
// asks for at least a minute after a secondary limit.
const defaultWait = 60

// Errors of a call. None of them holds the token or the text of the answer.
var (
	// ErrUnauthorized means GitHub refused the token (401).
	ErrUnauthorized = errors.New("github refused the token")
	// ErrForbidden means the token lacks a permission or the repository is out of its reach (403).
	ErrForbidden = errors.New("the token has no permission for this")
	// ErrNotFound means the thing does not exist, or the token cannot see it (404).
	ErrNotFound = errors.New("not found or the token cannot see it")
	// ErrInvalid means GitHub could not process the request (422 and other 4xx).
	ErrInvalid = errors.New("github refused the request")
	// ErrUnavailable means a failure on the way or on the GitHub side.
	ErrUnavailable = errors.New("github is not reachable now")
	// ErrUnreadable means a change was accepted but its answer could not be read.
	ErrUnreadable = errors.New("github accepted the change but its answer was not readable")
	// ErrTooLarge means an answer above MaxBody.
	ErrTooLarge = errors.New("the answer of github is too large")
)

// StatusError is a refusal of GitHub with the status it answered. It wraps one
// of the errors above, so errors.Is keeps working, and holds no text of the
// answer.
type StatusError struct {
	Status int
	Err    error
}

func (e *StatusError) Error() string { return e.Err.Error() }
func (e *StatusError) Unwrap() error { return e.Err }

// StatusOf returns the HTTP status that GitHub answered for err, or 0 when err
// is not a refusal of GitHub (a transport failure, a limit).
func StatusOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// Client calls the API. The zero value is not ready; use New.
type Client struct {
	// Base is the address of the API without a trailing slash.
	Base string
	// HTTP refuses redirects (pkg/httpclient does by default).
	HTTP *http.Client
	// Now is the clock; a test fixes it.
	Now func() time.Time
}

// New returns a client of the real API.
func New() *Client {
	return &Client{Base: DefaultBase, HTTP: httpclient.NewWithTimeout(30 * time.Second), Now: time.Now}
}

// Response is the part of an answer a caller needs besides the body.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// NextPage reports whether the Link header offers a next page.
func (r Response) NextPage() bool {
	for _, part := range strings.Split(r.Header.Get("Link"), ",") {
		if strings.Contains(part, `rel="next"`) {
			return true
		}
	}
	return false
}

// Get calls a path of the API with the token. accept is the media type
// asked for; empty means JSON. A non-2xx answer is an error from the list
// above, or a *contract.ErrRateLimited.
func (c *Client) Get(ctx context.Context, token, path string, query url.Values, accept string) (Response, error) {
	return c.do(ctx, http.MethodGet, token, path, query, accept, nil)
}

// SendJSON calls a path with a method that changes something (POST, PATCH),
// with body encoded as JSON, and decodes the answer into v when v is not nil.
// A change is never repeated here: a failed attempt is reported to the caller,
// which decides, since GitHub has no idempotency key.
func (c *Client) SendJSON(ctx context.Context, method, token, path string, body, v any) (Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return Response{}, ErrInvalid
	}
	r, err := c.do(ctx, method, token, path, nil, "", raw)
	if err != nil {
		return r, err
	}
	if v != nil {
		if err := json.Unmarshal(r.Body, v); err != nil {
			// The change went through; only the answer is unreadable.
			return r, ErrUnreadable
		}
	}
	return r, nil
}

func (c *Client) do(ctx context.Context, method, token, path string, query url.Values, accept string, payload []byte) (Response, error) {
	if token == "" {
		return Response{}, ErrUnauthorized
	}
	u := strings.TrimRight(c.Base, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return Response{}, ErrInvalid
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "LocalAI-github-addon")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		// The transport error names the URL, never a header, but it is not
		// worth keeping either: the class is what a caller can act on.
		return Response{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return Response{}, ErrUnavailable
	}
	if len(body) > MaxBody {
		return Response{}, ErrTooLarge
	}
	out := Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return out, nil
	}
	return out, c.failure(resp.StatusCode, resp.Header, body)
}

// GetJSON is Get with the JSON body decoded into v.
func (c *Client) GetJSON(ctx context.Context, token, path string, query url.Values, v any) (Response, error) {
	r, err := c.Get(ctx, token, path, query, "")
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return r, ErrUnavailable
	}
	return r, nil
}

// failure maps a non-2xx answer. It reads the rate limit headers: GitHub
// answers 403 or 429 with x-ratelimit-remaining 0 and x-ratelimit-reset (epoch
// seconds) for the primary limit, and retry-after (seconds) or a message for
// the secondary limit.
func (c *Client) failure(status int, h http.Header, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return &StatusError{Status: status, Err: ErrUnauthorized}
	case http.StatusForbidden, http.StatusTooManyRequests:
		if wait, limited := c.limitWait(status, h, body); limited {
			return &contract.ErrRateLimited{RetryAfter: wait}
		}
		return &StatusError{Status: status, Err: ErrForbidden}
	case http.StatusNotFound, http.StatusGone:
		return &StatusError{Status: status, Err: ErrNotFound}
	}
	if status >= 500 {
		return ErrUnavailable
	}
	return &StatusError{Status: status, Err: ErrInvalid}
}

func (c *Client) limitWait(status int, h http.Header, body []byte) (int, bool) {
	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return min(max(n, 1), contract.MaxRetryAfter), true
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if epoch, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			wait := int(epoch - c.now().Unix())
			return min(max(wait, 1), contract.MaxRetryAfter), true
		}
		return defaultWait, true
	}
	if status == http.StatusTooManyRequests {
		return defaultWait, true
	}
	// A secondary limit is a 403 whose message says so. The message is read
	// here and never returned.
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) == nil {
		l := strings.ToLower(m.Message)
		if strings.Contains(l, "secondary rate limit") || strings.Contains(l, "abuse detection") || strings.Contains(l, "rate limit exceeded") {
			return defaultWait, true
		}
	}
	return 0, false
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Describe is a fixed sentence for an error of this package, for a log line or
// a refusal. It exists so no caller formats the error of a transport.
func Describe(err error) string {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return ErrUnauthorized.Error()
	case errors.Is(err, ErrForbidden):
		return ErrForbidden.Error()
	case errors.Is(err, ErrNotFound):
		return ErrNotFound.Error()
	case errors.Is(err, ErrInvalid):
		return ErrInvalid.Error()
	case errors.Is(err, ErrTooLarge):
		return ErrTooLarge.Error()
	}
	return fmt.Sprint(ErrUnavailable)
}
