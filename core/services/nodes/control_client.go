package nodes

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
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ControlClient sends the control verbs of a frontend to a worker as HTTP
// requests, over whatever dialer reaches that worker. In a tunnel deployment the
// dialer opens a stream with the http tag, so the request reaches the HTTP
// server that the worker already runs and no address of the worker is needed.
//
// There is one client for the frontend and not one for each verb, because the
// mapping from a failed request onto the four conditions of
// .agents/distributed-seams.md belongs in one place. See controlFailure.
type ControlClient struct {
	// dialFor supplies the transport for one worker. It is per node because an
	// http.Transport carries one DialContext, and a worker is reached through its
	// own tunnel. A nil dialFor refuses every call.
	dialFor WorkerNetDialerFor
	token   string

	// clients holds one HTTP client for each node, so that two verbs in a row
	// reuse the stream that the transport already holds. ForgetNode drops one.
	clientsMu sync.Mutex
	clients   map[string]*nodeHTTPClient
}

// nodeHTTPClient is a client and the transport it was built on. The transport is
// kept beside the client because closing the idle streams of a node needs it,
// and what http.Client.Transport holds is whatever the shared constructor wrapped
// it in.
type nodeHTTPClient struct {
	client    *http.Client
	transport *http.Transport
}

// NewControlClient returns the client for the workers that dialFor can reach. It
// authenticates with the registration token of the deployment, as the file
// transfer to a worker does.
func NewControlClient(dialFor WorkerNetDialerFor, token string) *ControlClient {
	return &ControlClient{dialFor: dialFor, token: token, clients: map[string]*nodeHTTPClient{}}
}

// WorkerHTTPHost is the host of the URL of a request to the HTTP server of a
// worker. A worker that has an address gets it. A worker that holds a tunnel has
// none, and gets a name under the reserved suffix .invalid, which resolves
// nowhere. The name is never dialled: the dialer of the transport opens the
// stream, and the host exists because a request needs one and a log line should
// say which node it was for.
func WorkerHTTPHost(nodeID, addr string) string {
	if addr != "" {
		return addr
	}
	return nodeID + tunnelOnlyHostSuffix
}

// tunnelOnlyHostSuffix ends the host that WorkerHTTPHost gives to a worker that
// has no address. .invalid is reserved and resolves nowhere.
const tunnelOnlyHostSuffix = ".worker.invalid"

// IsTunnelOnlyHost reports whether a host is one that WorkerHTTPHost made for a
// worker with no address, which only a tunnel can reach.
func IsTunnelOnlyHost(host string) bool {
	return strings.HasSuffix(host, tunnelOnlyHostSuffix)
}

// clientFor returns the HTTP client for a node, and builds it on first use.
//
// HTTP/2 is off, as it is for the file stager: its flow control stalls a long
// stream, and an install streams for minutes. No timeout is set on the client,
// because it would bound the body, and the body of an install stays open for as
// long as the install runs. What bounds a call is the context of its caller.
func (c *ControlClient) clientFor(nodeID string) (*http.Client, error) {
	if c == nil || c.dialFor == nil {
		return nil, fmt.Errorf("control request to node %q: no dialer is configured", nodeID)
	}
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if cl, ok := c.clients[nodeID]; ok {
		return cl.client, nil
	}
	dial := c.dialFor(nodeID)
	if dial == nil {
		return nil, fmt.Errorf("control request to node %q: no dialer for the node", nodeID)
	}
	transport := &http.Transport{
		DialContext:           dial,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	cl := httpclient.New(httpclient.WithTransport(transport))
	c.clients[nodeID] = &nodeHTTPClient{client: cl, transport: transport}
	return cl, nil
}

// ForgetNode drops the client of a node and closes the idle streams it holds.
// It is safe on a nil receiver.
func (c *ControlClient) ForgetNode(nodeID string) {
	if c == nil {
		return
	}
	c.clientsMu.Lock()
	entry, ok := c.clients[nodeID]
	delete(c.clients, nodeID)
	c.clientsMu.Unlock()
	if ok {
		entry.transport.CloseIdleConnections()
	}
}

// Call sends one request and decodes the reply. reply may be nil for a verb that
// answers with no body.
//
// A reply with its Error field set is not an error here: the worker answered,
// and what it said is for the caller to read. Only a failure to reach the worker
// or to read its answer comes back as an error.
func (c *ControlClient) Call(ctx context.Context, nodeID, verb string, req, reply any) error {
	resp, err := c.do(ctx, nodeID, verb, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent || reply == nil {
		// Drained, so that the transport keeps the stream for the next verb.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxControlErrorBodyBytes))
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		return controlFailure(ctx, nodeID, fmt.Errorf("decoding the %s reply: %w", verb, err))
	}
	return nil
}

// CallStreaming sends a request whose answer is a stream of lines: zero or more
// progress lines and then one reply line. It calls onProgress for each progress
// line, in the order the worker sent them, with the subject the worker named for
// it (empty for a line meant for this caller alone), and decodes the reply line
// into reply. onProgress may be nil.
//
// onProgress runs on the goroutine of the caller. A slow callback holds up the
// body of this request and nothing else.
func (c *ControlClient) CallStreaming(ctx context.Context, nodeID, verb string, req, reply any, onProgress func(subject string, raw json.RawMessage)) error {
	resp, err := c.do(ctx, nodeID, verb, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	dec := json.NewDecoder(resp.Body)
	var terminal json.RawMessage
	for terminal == nil {
		var env workerctl.Envelope
		if err := dec.Decode(&env); err != nil {
			// The body ended before the reply line. That is the link breaking in
			// the middle of the verb, and reading it as "the install failed" would
			// be a verdict about the backend that nothing learned.
			if errors.Is(err, io.EOF) {
				err = fmt.Errorf("the %s stream ended before its reply line: %w", verb, io.ErrUnexpectedEOF)
			}
			return controlFailure(ctx, nodeID, err)
		}
		if env.Reply != nil {
			terminal = env.Reply
			break
		}
		if env.Progress != nil && onProgress != nil {
			onProgress(env.Subject, env.Progress)
		}
	}
	if reply == nil {
		return nil
	}
	if err := json.Unmarshal(terminal, reply); err != nil {
		return controlFailure(ctx, nodeID, fmt.Errorf("decoding the %s reply line: %w", verb, err))
	}
	return nil
}

// do sends the request and returns the response for a status that carries an
// answer. The caller closes the body.
func (c *ControlClient) do(ctx context.Context, nodeID, verb string, req any) (*http.Response, error) {
	client, err := c.clientFor(nodeID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, controlFailure(ctx, nodeID, fmt.Errorf("encoding the %s request: %w", verb, err))
	}
	path := workerctl.PathOf(verb)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+WorkerHTTPHost(nodeID, "")+path, bytes.NewReader(body))
	if err != nil {
		return nil, controlFailure(ctx, nodeID, fmt.Errorf("building the %s request: %w", verb, err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, controlFailure(ctx, nodeID, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return resp, nil
	case http.StatusNotFound:
		// The worker answered about itself and not about a backend: it is older
		// than this frontend and serves no such verb. The catch-all of the control
		// plane gives a body that says so, which tells it from a fault of a proxy.
		_ = resp.Body.Close()
		return nil, fmt.Errorf("control request %s to node %q: %w", path, nodeID, errVerbNotServed)
	default:
		// A verb's own failure is a 200 with the reply. A status that is not 2xx
		// is the worker failing to read or route the request, and none of those
		// is evidence about a backend.
		detail := readErrorBody(resp)
		_ = resp.Body.Close()
		if workerctl.IsBusy(resp.StatusCode, detail) {
			return nil, controlFailure(ctx, nodeID, fmt.Errorf("the %s verb: %w", verb, ErrWorkerBusy))
		}
		return nil, controlFailure(ctx, nodeID, fmt.Errorf("the %s verb answered HTTP %d: %s", verb, resp.StatusCode, detail))
	}
}

// ErrWorkerBusy is workerctl.ErrWorkerBusy: the worker answered that it has no
// free slot for a run.
var ErrWorkerBusy = workerctl.ErrWorkerBusy

// maxControlErrorBodyBytes bounds how much of an error body reaches a log line.
const maxControlErrorBodyBytes = 512

func readErrorBody(resp *http.Response) string {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxControlErrorBodyBytes))
	if err != nil {
		return "<the body could not be read>"
	}
	return string(bytes.TrimSpace(raw))
}

// controlFailure names the node in the error of a control request and keeps the
// chain of the cause intact, with one rule before it: the budget of the caller is
// checked first.
//
// A cause that is ErrNoRoute stays ErrNoRoute, because the dialer put it there
// and only the dialer knows that no route exists. A cause that is a refusal of
// the worker stays what it was. Nothing else is promoted: a request that failed
// for any other reason is a plain error, which is never a route and never a
// verdict about a backend.
//
// The expired budget is checked first because a worker's refusal that arrives in
// the instant the deadline passes would otherwise read as the worker's answer,
// and nothing orders the two timers. An expiry says nothing about a worker.
func controlFailure(ctx context.Context, nodeID string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := callerRanOut(ctx); ctxErr != nil {
		return fmt.Errorf("control request to node %q: the budget of the caller ran out: %w", nodeID, ctxErr)
	}
	return fmt.Errorf("control request to node %q: %w", nodeID, err)
}

// callerRanOut reports whether the budget of the caller ended an attempt. It
// answers from the wall clock as well as from the context, because the timer of
// the socket and the timer of the context are two timers and nothing orders
// them: a socket can report its timeout before ctx.Err stops returning nil.
func callerRanOut(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
