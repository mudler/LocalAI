// SPDX-License-Identifier: MIT
package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is an incarnation-bound capability. Only the supervisor creates one.
// Callers must obtain Authority from host authentication, never model arguments.
type Client struct {
	mu     sync.Mutex
	accept bool
	active int
	// polls counts the connector long polls in flight. They have a pool of
	// their own (maxPolls) so that a binding that waits up to 25 s does not
	// take a slot from the short calls (see admitIn).
	polls int
	// clientCalls counts the client route calls in flight. A route may run for
	// a minute, so these calls have a pool of their own (clientCallLimit):
	// paired clients cannot take every slot of the short calls that the chat
	// tool calls of the add-on use.
	clientCalls          int
	observationLeases    int
	observations         map[string]context.CancelFunc
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
	return &Client{inflight: hostops.NewInFlight(), endpoint: r.Endpoint, credential: credential, registration: r, limits: l, http: httpclient.New(httpclient.WithTransport(t))}, nil
}
func (c *Client) admitting() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.accept }
func (c *Client) withdraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accept = false
	for _, cancel := range c.observations {
		cancel()
	}
}
func (c *Client) drain(ctx context.Context) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.Lock()
		n := c.active + c.polls + c.clientCalls + c.observationLeases
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

// maxPolls bounds the connector long polls in flight to one add-on. The host
// keeps one poll per binding, so this is also the most pull bindings of one
// add-on. A poll holds its slot for up to contract.MaxPollSeconds, which is
// why it must not draw on limits.Concurrency, the budget of the short calls.
const maxPolls = 64

// pool names the budget a call draws on.
type pool int

const (
	poolCall   pool = iota // short calls: read, act, preview, connector pushes
	poolPoll               // connector long polls and client streams
	poolClient             // client route calls that do not stream
)

// clientCallLimit is how many client route calls may run at once in one
// add-on: half of the short call budget, and at least one. The other half stays
// for the tool calls of chats. A client call also counts in the short call
// budget (active), so the sum of both never passes limits.Concurrency.
func (c *Client) clientCallLimit() int {
	return int(max(1, c.limits.Concurrency/2))
}

func (c *Client) admit() (func(), error) { return c.admitIn(poolCall) }

// admitIn admits one call into the pool of short calls, the poll pool or the
// client call pool. All pools stop at withdraw, and the returned function
// frees the slot once.
func (c *Client) admitIn(p pool) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.accept {
		return nil, ErrNotSent
	}
	switch p {
	case poolClient:
		// A client call takes a slot of the short call budget, so the add-on
		// never gets more calls than it declared, and it takes no more than
		// half of them, so the tool calls of chats keep the other half.
		if c.clientCalls >= c.clientCallLimit() || int64(c.active) >= c.limits.Concurrency {
			return nil, ErrNotSent
		}
		c.clientCalls++
		c.active++
		var once sync.Once
		return func() { once.Do(func() { c.mu.Lock(); c.clientCalls--; c.active--; c.mu.Unlock() }) }, nil
	case poolPoll:
		if c.polls >= maxPolls {
			return nil, ErrNotSent
		}
		c.polls++
		var once sync.Once
		return func() { once.Do(func() { c.mu.Lock(); c.polls--; c.mu.Unlock() }) }, nil
	}
	if int64(c.active) >= c.limits.Concurrency {
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
		wait := resp.Header.Get("Retry-After")
		upstream, _ := strconv.Atoi(resp.Header.Get(contract.HeaderUpstreamStatus))
		if !contract.ValidUpstreamStatus(upstream) {
			upstream = 0
		}
		hint := contract.CleanHint(resp.Header.Get(contract.HeaderUpstreamHint))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && reason == "rate_limited" {
			// The service behind the add-on asks the caller to wait. The call
			// had no effect and the same call may succeed later.
			n, _ := strconv.Atoi(wait)
			return nil, contract.RateLimitedAfter(n)
		}
		if resp.StatusCode == http.StatusConflict {
			return nil, ErrConflict
		}
		if resp.StatusCode == http.StatusForbidden && reason == "forbidden" {
			// The answer of an add-on runtime to a permission it checked. A 403
			// with another body is the envelope check, a host fault.
			return nil, ErrDenied
		}
		if resp.StatusCode == http.StatusUnauthorized && reason == "credential_rejected" {
			// The service refused the access token and the call changed
			// nothing. Any other 401 is not this answer and stays an outage.
			return nil, ErrCredentialRejected
		}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
			r := &Refusal{Status: resp.StatusCode, Reason: reason}
			if upstream != 0 {
				r.Upstream, r.Hint = upstream, hint
			}
			return nil, r
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

// begin records the envelope of a call for the host operation channel, with
// the chat the call was made for when ctx carries one (hostops.WithBinding).
// The binding stays in the host's record: the envelope sent to the add-on is
// the same with or without it. The returned function ends the record.
func (c *Client) begin(ctx context.Context, env *contract.Envelope) func() {
	if c.inflight == nil || env == nil {
		return func() {}
	}
	if b, ok := hostops.BindingOf(ctx); ok {
		return c.inflight.BeginRuntime(*env, b, true, c.registration.Incarnation)
	}
	return c.inflight.BeginRuntime(*env, hostops.Binding{}, false, c.registration.Incarnation)
}
func (c *Client) call(ctx context.Context, path string, env *contract.Envelope, in, out any, timeout time.Duration) error {
	return c.callIn(poolCall, ctx, path, env, in, out, timeout)
}
func (c *Client) callIn(p pool, ctx context.Context, path string, env *contract.Envelope, in, out any, timeout time.Duration) error {
	return c.callThen(p, ctx, path, env, in, out, timeout, nil)
}

// callThen is callIn with a step that runs after a good answer and before the
// envelope stops being live: a job lease must take over a request that is
// still in flight.
func (c *Client) callThen(p pool, ctx context.Context, path string, env *contract.Envelope, in, out any, timeout time.Duration, then func()) error {
	done, e := c.admitIn(p)
	if e != nil {
		return e
	}
	defer done()
	defer c.begin(ctx, env)()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, e := c.send(ctx, path, in)
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	if e = decode(resp.Body, c.limits.EnvelopeBytes, out); e == nil && then != nil {
		then()
	}
	return e
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

var _ contract.ActRuntime = (*Client)(nil)

// Act runs an action tool. It is sent only to an add-on that declared actions,
// with the act action and the act scope, and it may run up to
// contract.ActTimeout. It is never retried here: a call that may have run is
// the caller's to report as an unknown outcome.
func (c *Client) Act(ctx context.Context, v contract.Envelope) (contract.Result, error) {
	var r contract.Result
	if v.Request.Action != "act" || !c.registration.Capabilities.Actions {
		return r, ErrNotSent
	}
	if e := c.valid(v, "act"); e != nil {
		return r, e
	}
	slot, refusal := c.reserveJobLease(v)
	if refusal != nil {
		return *refusal, nil
	}
	defer slot.Release()
	e := c.callThen(poolCall, ctx, "/act", &v, v, &r, contract.ActTimeout, func() {
		if r.Validate(c.limits) == nil {
			c.leaseJobs(v, r, slot)
		}
	})
	if e == nil {
		e = r.Validate(c.limits)
	}
	return r, e
}

var _ contract.ConnectorRuntime = (*Client)(nil)

// Connector calls the connector route of an add-on that declared the
// connector capability. The call carries no envelope: it is the host's own,
// made for a binding, and the add-on authenticates the host by the credential
// of the incarnation. A call that may have run (a send) is not retried here.
func (c *Client) Connector(ctx context.Context, v contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	var r contract.ConnectorResponse
	if c.registration.Capabilities.Connector == nil {
		return r, ErrNotSent
	}
	p := poolCall
	if v.Op == contract.ConnectorOpPoll {
		p = poolPoll
	}
	e := c.callIn(p, ctx, "/connector", nil, v, &r, v.CallTimeout())
	return r, e
}

var (
	_ contract.ClientCaller      = (*Client)(nil)
	_ contract.ClientRouteSource = (*Client)(nil)
)

// ClientRoutes are the routes the running incarnation declared for clients.
func (c *Client) ClientRoutes() []contract.ClientRoute {
	if d := c.registration.Capabilities.Client; d != nil {
		return append([]contract.ClientRoute(nil), d.Routes...)
	}
	return nil
}

// clientRoute finds the declared route a request names and checks the request
// against it, so a request that the host did not match is never sent.
func (c *Client) clientRoute(v contract.ClientRequest, stream bool) (contract.ClientRoute, error) {
	for _, r := range c.ClientRoutes() {
		if r.ID != v.RouteID {
			continue
		}
		if r.Method != v.Method || r.Stream != stream || int64(len(v.Body)) > r.BodyLimit() ||
			contract.ValidateClientQuery(v.Query) != nil || !contract.ValidEventID(v.LastEventID) || v.Client.ID == "" {
			return r, ErrNotSent
		}
		return r, nil
	}
	return contract.ClientRoute{}, ErrNotSent
}

// clientEnvelope is the host's record of a live client call. It never goes to
// the add-on: it is what the host operations of the call are checked against.
// The authority is a read scope, so the call can reach only the operations of
// the client family (see the broker), and it lasts as long as the call.
func (c *Client) clientEnvelope(actor contract.Actor, op string, life time.Duration) (contract.Envelope, error) {
	if !strings.HasPrefix(actor.CredentialID, contract.ClientCredentialPrefix) || actor.UserID == "" {
		return contract.Envelope{}, ErrNotSent
	}
	return contract.Envelope{
		Request:   contract.Request{AddonID: c.registration.AddonID, OperationID: op, Generation: c.registration.Generation, Action: "client", Arguments: json.RawMessage(`{}`)},
		Authority: contract.Authority{Actor: actor, Scope: "read", ExpiresAt: time.Now().Add(life + time.Minute)},
	}, nil
}

func newClientOperationID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrNotSent
	}
	return "cl-" + hex.EncodeToString(b[:]), nil
}

// ClientCallAs calls a client route that does not stream. It is not retried here: a
// call that may have run is the caller's to report.
func (c *Client) ClientCallAs(ctx context.Context, actor contract.Actor, v contract.ClientRequest) (contract.ClientResponse, error) {
	var r contract.ClientResponse
	route, e := c.clientRoute(v, false)
	if e != nil {
		return r, e
	}
	if v.OperationID, e = newClientOperationID(); e != nil {
		return r, e
	}
	env, e := c.clientEnvelope(actor, v.OperationID, route.Timeout())
	if e != nil {
		return r, e
	}
	if e = c.callIn(poolClient, ctx, "/client", &env, v, &r, route.Timeout()); e == nil {
		e = r.Validate(c.limits)
	}
	return r, e
}

// ClientStreamAs opens a client route that streams. A stream holds a slot of
// the long call pool until it closes, and ends after
// contract.MaxClientStreamSeconds. The live record of the call lasts as long as
// the stream, so the add-on may make host operations while it serves it.
func (c *Client) ClientStreamAs(ctx context.Context, actor contract.Actor, v contract.ClientRequest) (io.ReadCloser, error) {
	if _, e := c.clientRoute(v, true); e != nil {
		return nil, e
	}
	var e error
	if v.OperationID, e = newClientOperationID(); e != nil {
		return nil, e
	}
	env, e := c.clientEnvelope(actor, v.OperationID, contract.MaxClientStreamSeconds*time.Second)
	if e != nil {
		return nil, e
	}
	done, e := c.admitIn(poolPoll)
	if e != nil {
		return nil, e
	}
	end := c.begin(ctx, &env)
	ctx, cancel := context.WithTimeout(ctx, contract.MaxClientStreamSeconds*time.Second)
	resp, e := c.send(ctx, "/client/stream", v)
	if e != nil {
		cancel()
		end()
		done()
		return nil, e
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		_ = resp.Body.Close()
		cancel()
		end()
		done()
		return nil, ErrRuntime
	}
	stream := &boundedStream{body: resp.Body, remaining: 1 << 50, finish: func() { cancel(); end(); done() }}
	context.AfterFunc(ctx, func() { _ = stream.Close() })
	return stream, nil
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
	end := c.begin(ctx, &v)
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
