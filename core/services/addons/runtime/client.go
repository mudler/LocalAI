// SPDX-License-Identifier: MIT
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is an incarnation-bound capability. Only the supervisor creates one.
// Callers must obtain Authority from host authentication, never model arguments.
type Client struct {
	mu                   sync.Mutex
	accept               bool
	active               int
	endpoint, credential string
	registration         Registration
	limits               contract.Limits
	http                 *http.Client
	// inflight, when set, records the envelope of each call while it runs, so
	// a host operation of this add-on can find the request it belongs to.
	inflight *hostops.InFlight
}

var _ contract.Runtime = (*Client)(nil)

func newClient(r Registration, credential string, l contract.Limits) (*Client, error) {
	u, e := url.Parse(r.Endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || len(credential) != 64 {
		return nil, ErrRuntime
	}
	t := httpclient.HardenedTransport()
	t.Proxy = nil
	t.MaxResponseHeaderBytes = 16 << 10
	return &Client{endpoint: r.Endpoint, credential: credential, registration: r, limits: l, http: httpclient.New(httpclient.WithTransport(t))}, nil
}
func (c *Client) admitting() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.accept }
func (c *Client) withdraw()       { c.mu.Lock(); c.accept = false; c.mu.Unlock() }
func (c *Client) drain(ctx context.Context) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.Lock()
		n := c.active
		c.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *Client) admit() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.accept || int64(c.active) >= c.limits.Concurrency {
		return nil, ErrNotSent
	}
	c.active++
	var once sync.Once
	return func() { once.Do(func() { c.mu.Lock(); c.active--; c.mu.Unlock() }) }, nil
}
func (c *Client) valid(e contract.Envelope, scope string) error {
	if e.Validate(time.Now(), c.limits) != nil || e.Authority.Scope != scope || e.Request.AddonID != c.registration.AddonID || e.Request.Generation != c.registration.Generation {
		return ErrNotSent
	}
	return nil
}
func (c *Client) send(ctx context.Context, path string, in any) (*http.Response, error) {
	b, e := json.Marshal(in)
	if e != nil || int64(len(b)) > c.limits.EnvelopeBytes {
		return nil, ErrNotSent
	}
	// wrote is set once the request headers are on the connection. Until then
	// the add-on cannot have seen the request, so a failure is ErrNotSent.
	var wrote atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) }})
	req, e := http.NewRequestWithContext(ctx, "POST", c.endpoint+path, bytes.NewReader(b))
	if e != nil {
		return nil, ErrNotSent
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	req.Header.Set("X-LocalAI-Incarnation", c.registration.Incarnation)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		if !wrote.Load() {
			return nil, ErrNotSent
		}
		return nil, ErrRuntime
	}
	if resp.StatusCode != http.StatusOK {
		reason := refusalReason(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			return nil, ErrConflict
		}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
			return nil, &Refusal{Status: resp.StatusCode, Reason: reason}
		}
		return nil, ErrRuntime
	}
	return resp, nil
}

// maxReason bounds the reason a refusal carries.
const maxReason = 64

// refusalReason reads a short printable class from an error answer. Only
// ASCII letters, digits, space and a few marks survive, so the reason never
// carries control bytes or a long echo of the request.
func refusalReason(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, maxReason*4))
	out := make([]byte, 0, maxReason)
	for _, c := range b {
		if len(out) == maxReason {
			break
		}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == ' ', c == '_', c == '-', c == '.':
			out = append(out, c)
		case c == '\n':
			return string(out)
		}
	}
	return strings.TrimSpace(string(out))
}
func decode(body io.Reader, max int64, out any) error {
	b, e := io.ReadAll(io.LimitReader(body, max+1))
	if e != nil || int64(len(b)) > max {
		return ErrRuntime
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrRuntime
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrRuntime
	}
	return nil
}
func (c *Client) health(ctx context.Context, r Registration) error {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	resp, e := c.send(ctx, "/health", struct{}{})
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	var got Registration
	if decode(resp.Body, c.limits.ManifestBytes, &got) != nil || !reflect.DeepEqual(got, r) {
		return ErrRuntime
	}
	return nil
}

// begin records the envelope of a call for the host operation channel. The
// returned function ends the record.
func (c *Client) begin(env *contract.Envelope) func() {
	if c.inflight == nil || env == nil {
		return func() {}
	}
	return c.inflight.Begin(*env)
}
func (c *Client) call(ctx context.Context, path string, env *contract.Envelope, in, out any, timeout time.Duration) error {
	done, e := c.admit()
	if e != nil {
		return e
	}
	defer done()
	defer c.begin(env)()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, e := c.send(ctx, path, in)
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	return decode(resp.Body, c.limits.EnvelopeBytes, out)
}
func (c *Client) Read(ctx context.Context, v contract.Envelope) (contract.Result, error) {
	var r contract.Result
	if e := c.valid(v, "read"); e != nil {
		return r, e
	}
	e := c.call(ctx, "/read", &v, v, &r, c.limits.ReadTimeout)
	if e == nil {
		e = r.Validate(c.limits)
	}
	return r, e
}
func (c *Client) Preview(ctx context.Context, v contract.Envelope) (contract.Preview, error) {
	var r contract.Preview
	if e := c.valid(v, "preview"); e != nil {
		return r, e
	}
	e := c.call(ctx, "/preview", &v, v, &r, c.limits.PreviewTimeout)
	if e == nil {
		e = r.Validate(time.Now(), c.limits)
		if r.OperationID != v.Request.OperationID {
			e = ErrRuntime
		}
	}
	return r, e
}

type ApplyRequest struct {
	Envelope contract.Envelope `json:"envelope"`
	Revision contract.Revision `json:"revision"`
}
type ReceiptRequest struct {
	OperationID string `json:"operationId"`
}
type OutputRequest struct {
	Envelope contract.Envelope `json:"envelope"`
	Handle   string            `json:"handle"`
}

func (c *Client) Apply(ctx context.Context, v contract.Envelope, revision contract.Revision) (contract.Receipt, error) {
	var r contract.Receipt
	if e := c.valid(v, "apply"); e != nil {
		return r, e
	}
	if revision.Schema == 0 {
		return r, ErrNotSent
	}
	e := c.call(ctx, "/apply", &v, ApplyRequest{v, revision}, &r, c.limits.ApplyTimeout)
	if e == nil {
		e = r.Validate(c.limits)
		if r.OperationID != v.Request.OperationID {
			e = ErrRuntime
		}
	}
	return r, e
}
func (c *Client) Receipt(ctx context.Context, id string) (contract.Receipt, error) {
	var r contract.Receipt
	if (contract.Receipt{OperationID: id, State: contract.ReceiptUnknown}).Validate(c.limits) != nil {
		return r, ErrRuntime
	}
	e := c.call(ctx, "/receipt", nil, ReceiptRequest{id}, &r, c.limits.ReadTimeout)
	if e == nil {
		e = r.Validate(c.limits)
		if r.OperationID != id {
			e = ErrRuntime
		}
	}
	return r, e
}
func (c *Client) OpenOutput(ctx context.Context, v contract.Envelope, handle string) (io.ReadCloser, error) {
	if e := c.valid(v, "output"); e != nil {
		return nil, e
	}
	if (contract.Receipt{OperationID: handle, State: contract.ReceiptUnknown}).Validate(c.limits) != nil {
		return nil, ErrRuntime
	}
	admitted, e := c.admit()
	if e != nil {
		return nil, e
	}
	// The record lasts as long as the stream, like the admission slot.
	end := c.begin(&v)
	done := func() { end(); admitted() }
	ctx, cancel := context.WithTimeout(ctx, c.limits.ReadTimeout)
	resp, e := c.send(ctx, "/output", OutputRequest{v, handle})
	if e != nil {
		cancel()
		done()
		return nil, e
	}
	if resp.ContentLength > c.limits.ArtifactBytes {
		_ = resp.Body.Close()
		cancel()
		done()
		return nil, ErrRuntime
	}
	stream := &boundedStream{body: resp.Body, remaining: c.limits.ArtifactBytes, finish: func() { cancel(); done() }}
	context.AfterFunc(ctx, func() { _ = stream.Close() })
	return stream, nil
}

type boundedStream struct {
	body      io.ReadCloser
	remaining int64
	once      sync.Once
	finish    func()
}

func (s *boundedStream) Read(p []byte) (int, error) {
	if s.remaining == 0 {
		var b [1]byte
		n, e := s.body.Read(b[:])
		_ = s.Close()
		if n > 0 {
			return 0, ErrRuntime
		}
		return 0, e
	}
	if int64(len(p)) > s.remaining {
		p = p[:s.remaining]
	}
	n, e := s.body.Read(p)
	s.remaining -= int64(n)
	if e != nil {
		_ = s.Close()
	}
	return n, e
}
func (s *boundedStream) Close() error {
	var e error
	s.once.Do(func() { e = s.body.Close(); s.finish() })
	return e
}
