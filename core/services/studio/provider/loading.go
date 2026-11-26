package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mudler/cogito"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/model"
)

// DefaultLoadWait is how long one turn waits, in total, for a model that is
// still loading. A large model can take many minutes to reach a worker in
// distributed mode, and the user would rather wait than retry by hand.
const DefaultLoadWait = 15 * time.Minute

// DefaultLoadRetry is the wait between two tries when the server says the
// model is loading but not when to ask again.
const DefaultLoadRetry = 5 * time.Second

// The wait between two tries stays in this range, whatever the server says. A
// shorter wait only asks a load that cannot be done yet; a longer one leaves a
// model that became ready idle.
const (
	minLoadRetry = time.Second
	maxLoadRetry = time.Minute
)

// peekLimit is how much of an answer is read to tell a loading model from a
// failure. The answers in question are short JSON objects.
const peekLimit = 64 << 10

// Loading is what the server said about a model that is still loading.
type Loading struct {
	// Model, State and Node come from the answer's `loading` object, when it
	// has one. Progress is in percent.
	Model    string
	State    string
	Node     string
	Progress float64
	// Message is the server's own sentence, and Code the error code of the
	// answer ("model_loading" for a load in progress).
	Message string
	Code    string
	// RetryAfter is how long the client waits before it asks again.
	RetryAfter time.Duration
	// Waited is how long the turn has already waited for the model.
	Waited time.Duration
}

// loadingBody is the part of a 503 or 429 answer that tells a loading or busy
// model from a failure.
type loadingBody struct {
	Error struct {
		Code    json.RawMessage `json:"code"`
		Type    string          `json:"type"`
		Message string          `json:"message"`
	} `json:"error"`
	Loading *struct {
		Model     string  `json:"model"`
		State     string  `json:"state"`
		Node      string  `json:"node"`
		Progress  float64 `json:"progress"`
		LastError string  `json:"last_error"`
	} `json:"loading"`
}

// codeModelLoading is the error code of a 503 for a model whose load is in
// progress, and loadStateFailed the load state of a load that failed and
// still holds its node.
const (
	codeModelLoading = "model_loading"
	loadStateFailed  = "failed"
)

// code returns the error code of the answer as text, whether the server sent
// it as a string or a number.
func (b loadingBody) code() string {
	var s string
	if json.Unmarshal(b.Error.Code, &s) == nil {
		return s
	}
	return string(b.Error.Code)
}

// heldFailure reports whether the answer is for a load that failed: the
// server holds the model for a short window after a failure and answers 503
// model_loading with the failed state. It returns the cause.
func (b loadingBody) heldFailure() (string, bool) {
	if b.Loading == nil || b.Loading.State != loadStateFailed {
		return "", false
	}
	if b.Loading.LastError != "" {
		return b.Loading.LastError, true
	}
	return b.Error.Message, true
}

// isLoading says whether resp asks the client to come back later: a 503 for a
// model that is loading, or a 503 or 429 with a Retry-After header. A model in
// cooldown after a failed load, a load that failed and still holds its node,
// and a spent quota are refusals, not waits.
func isLoading(resp *http.Response) bool {
	if resp.Header.Get(model.LoadFailedHeader) != "" {
		return false
	}
	switch resp.StatusCode {
	case http.StatusServiceUnavailable:
		b := peekBody(resp)
		if _, failed := b.heldFailure(); failed {
			return false
		}
		return resp.Header.Get("Retry-After") != "" || b.code() == codeModelLoading
	case http.StatusTooManyRequests:
		if resp.Header.Get("Retry-After") == "" {
			return false
		}
		// The quota answer has a Retry-After too, but it counts the hours to
		// the next quota window: that is not a wait for a turn.
		return peekBody(resp).Error.Type != "quota_exceeded"
	}
	return false
}

// peekBody reads the start of resp's body and puts it back, so the caller can
// still read the whole body.
func peekBody(resp *http.Response) loadingBody {
	var b loadingBody
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, peekLimit))
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(raw), resp.Body), Closer: resp.Body}
	_ = json.Unmarshal(raw, &b)
	return b
}

type readCloser struct {
	io.Reader
	io.Closer
}

// readLoading reads what a loading answer says. It does not close the body.
func readLoading(resp *http.Response) Loading {
	b := peekBody(resp)
	l := Loading{Message: b.Error.Message, Code: b.code(), RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	if b.Loading != nil {
		l.Model, l.State, l.Node, l.Progress = b.Loading.Model, b.Loading.State, b.Loading.Node, b.Loading.Progress
	}
	return l
}

// parseRetryAfter reads a Retry-After header: whole seconds or an HTTP date.
// An absent or unreadable value is DefaultLoadRetry. The result stays between
// minLoadRetry and maxLoadRetry.
func parseRetryAfter(v string, now time.Time) time.Duration {
	d := DefaultLoadRetry
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = at.Sub(now)
	}
	return min(max(d, minLoadRetry), maxLoadRetry)
}

// rewind returns a copy of req to send again, with a fresh body. It reports
// false when the body cannot be read again.
func rewind(req *http.Request) (*http.Request, bool) {
	next := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return next, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	b, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	next.Body = b
	return next, true
}

// sleepCtx waits for d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stillLoading is the error of a turn that waited its whole budget for a
// model that is still loading. It is not "the model failed": the load may
// well finish, and the user may try again later.
func stillLoading(name string, budget time.Duration) error {
	subject := "the model"
	if name != "" {
		subject += " " + strconv.Quote(name)
	}
	return cogito.Permanent(errors.New(subject + " is still loading after " + humanWait(budget) + " " + session.MarkStillLoading))
}

// notAvailable is the error of a turn that waited its whole budget on an
// answer that was not a load in progress: no node for the model, a model
// upgrade, a model at capacity. It names what the server said, since "the
// model is still loading" would send the user to wait for a load that does not
// exist.
func notAvailable(l Loading, budget time.Duration) error {
	subject := "the model"
	if l.Model != "" {
		subject += " " + strconv.Quote(l.Model)
	}
	cause := l.Message
	if cause == "" {
		cause = "the server asked to try again later"
	}
	return cogito.Permanent(errors.New(subject + " was not available after " + humanWait(budget) + ": " + cause))
}

func humanWait(d time.Duration) string {
	switch {
	case d == time.Minute:
		return "1 minute"
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", d/time.Minute)
	}
	return d.String()
}
