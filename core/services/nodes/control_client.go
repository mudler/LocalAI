package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ErrWorkerControlUnsupported reports that the worker answered, and does not
// serve that control verb.
//
// It is a deployment fact and not a verdict about anything: the worker is
// running a build older than this frontend, so a path this frontend knows is a
// 404 there. The one caller that may act on it is the upgrade fallback, which
// re-issues the legacy force-install the older build does understand.
//
// It is a SPECIALISATION of ErrWorkerUnroutable for the same reason
// ErrNoWorkerDialer is: everything that DELETES a node_models row makes exactly
// one check, and a 404 must not pass it. The worker spoke, but it said nothing
// about any backend, and reaping on it would evict healthy work over a version
// skew. Callers that want the fallback match this sentinel specifically, which
// ErrWorkerUnroutable does not imply in that direction.
var ErrWorkerControlUnsupported = fmt.Errorf("%w: the worker does not serve that control verb", ErrWorkerUnroutable)

// ControlClient issues the frontend's control RPCs to a worker over that
// worker's tunnel.
//
// It is the frontend half of core/services/workerctl: the worker mounts those
// paths on the loopback HTTP server it already runs, and this reaches them
// through the `http` stream tag, so a control RPC to a worker another replica
// holds is relayed exactly like an inference request.
//
// There is one of these per frontend rather than one per verb, because the
// mapping from a failed RPC onto the four conditions this system must never
// confuse belongs in ONE place. See controlFailure.
type ControlClient struct {
	// dialFor supplies the transport for one worker. It is per node because a
	// worker is reached over ITS OWN tunnel and an http.Transport carries one
	// DialContext, so a single shared transport could only ever reach one
	// worker. nil means no tunnel dialer is wired and every call is refused;
	// see ErrNoWorkerDialer for why that is not a fallback to a direct dial.
	dialFor WorkerNetDialerFor
	token   string

	// clients caches one *http.Client per node, so a verb issued twice in a
	// row reuses the tunnel stream its transport already holds instead of
	// opening a new one.
	//
	// Entries are never pruned, and that is judged rather than overlooked: the
	// map is bounded by the number of distinct workers this frontend has ever
	// commanded, which is bounded by the fleet, and a departed worker's entry
	// holds a map slot plus a transport whose idle connections IdleConnTimeout
	// reclaims. It is the same shape, and would need the same missing
	// node-departure signal to fix, as HTTPFileStager.clients.
	clientsMu sync.Mutex
	clients   map[string]*http.Client
}

// NewControlClient returns the control client for the workers dialFor can
// reach, authenticating with the deployment's registration token.
func NewControlClient(dialFor WorkerNetDialerFor, token string) *ControlClient {
	return &ControlClient{dialFor: dialFor, token: token, clients: map[string]*http.Client{}}
}

// clientFor returns the HTTP client that reaches one worker, building it on
// first use.
//
// The transport mirrors HTTPFileStager.clientFor, which is the other consumer
// of a worker's own HTTP server, and differs only where control traffic
// differs. HTTP/2 stays OFF for the reason it always was: its flow control
// stalls large transfers, and the install verb streams for minutes. The
// stager's 256 KB socket buffers are dropped because a control body is a
// handful of kilobytes and two 256 KB buffers per worker would be paid for
// every node in the fleet.
//
// The net.Dialer's connect timeout and keepalive have nothing to act on here:
// there is no TCP connect to time out, and liveness on the link is the yamux
// session's keepalive rather than the socket's. No client.Timeout is set
// either, because it would bound the response BODY, and the install verb's
// body stays open for as long as the install runs. What bounds a call is the
// context its caller passes.
func (c *ControlClient) clientFor(nodeID string) (*http.Client, error) {
	if c == nil || c.dialFor == nil {
		return nil, fmt.Errorf("control rpc to node %q: %w", nodeID, ErrNoWorkerDialer)
	}
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if cl, ok := c.clients[nodeID]; ok {
		return cl, nil
	}
	dial := c.dialFor(nodeID)
	if dial == nil {
		return nil, fmt.Errorf("control rpc to node %q: %w", nodeID, ErrNoWorkerDialer)
	}
	transport := &http.Transport{
		DialContext:           dial,
		ForceAttemptHTTP2:     false, // HTTP/2 flow control can stall a long streaming verb
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	cl := httpclient.New(httpclient.WithTransport(transport))
	c.clients[nodeID] = cl
	return cl, nil
}

// Call issues one control RPC and decodes its reply. reply may be nil for the
// verbs that answer 204.
//
// A reply that arrives with its Error field set is NOT an error here: the
// worker answered, and reading its answer is the caller's job. Only a failure
// to reach the worker, or an answer this frontend cannot read, comes back as an
// error, and every one of those is mapped by controlFailure.
func (c *ControlClient) Call(ctx context.Context, nodeID, path string, req, reply any) error {
	resp, err := c.do(ctx, nodeID, path, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent || reply == nil {
		// A verb that answers 204 sends no body at all; a caller that asked for
		// no reply may still have been sent one, and draining it is what lets
		// the transport keep the tunnel stream for the next verb instead of
		// tearing it down.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxControlErrorBodyBytes))
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		// A body this frontend cannot read is not the worker's verdict about
		// anything, so it must not be reported as one.
		return controlFailure(ctx, nodeID, fmt.Errorf("decoding the %s reply: %w", path, err))
	}
	return nil
}

// CallStreaming issues a control RPC whose response is an NDJSON envelope
// stream, invoking onProgress for each progress line and decoding the single
// terminal reply line into reply. onProgress may be nil.
//
// onProgress runs SYNCHRONOUSLY, on this goroutine. The NATS carrier ran each
// progress callback on a goroutine of its own because a slow callback there
// stalled the one reader thread every worker's events arrived on; here the only
// thing a slow callback holds up is this request's own body, which is the
// caller's business. Dropping the guard is also what makes the events arrive in
// the order the worker sent them.
func (c *ControlClient) CallStreaming(ctx context.Context, nodeID, path string,
	req, reply any, onProgress func(messaging.BackendInstallProgressEvent)) error {
	resp, err := c.do(ctx, nodeID, path, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	dec := json.NewDecoder(resp.Body)
	var terminal json.RawMessage
	for terminal == nil {
		var env workerctl.Envelope
		decErr := dec.Decode(&env)
		if decErr != nil {
			// EOF before a reply line is the tunnel dying mid-verb, and reading
			// it as "the install failed" is precisely the collapse this phase
			// exists to prevent: nothing was learned about the backend, so this
			// is unroutable and a caller may not act on it.
			if errors.Is(decErr, io.EOF) {
				decErr = fmt.Errorf("the %s stream ended before its reply line: %w", path, io.ErrUnexpectedEOF)
			}
			return controlFailure(ctx, nodeID, decErr)
		}
		if env.Reply != nil {
			terminal = env.Reply
			break
		}
		if env.Progress == nil || onProgress == nil {
			continue
		}
		var ev messaging.BackendInstallProgressEvent
		if err := json.Unmarshal(env.Progress, &ev); err != nil {
			// Progress is transient by contract, so a line this frontend cannot
			// read costs a tick and never the operation.
			xlog.Debug("unreadable control progress line", "node", nodeID, "path", path, "error", err)
			continue
		}
		onProgress(ev)
	}
	if reply == nil {
		return nil
	}
	if err := json.Unmarshal(terminal, reply); err != nil {
		return controlFailure(ctx, nodeID, fmt.Errorf("decoding the %s reply line: %w", path, err))
	}
	return nil
}

// do issues the request and returns the response for any status this frontend
// can read a body from. Every other outcome is already mapped.
//
// The caller owns closing the body.
func (c *ControlClient) do(ctx context.Context, nodeID, path string, req any) (*http.Response, error) {
	client, err := c.clientFor(nodeID)
	if err != nil {
		// ErrNoWorkerDialer already carries ErrWorkerUnroutable, so it must not
		// go through controlFailure, which would wrap the umbrella twice.
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, controlFailure(ctx, nodeID, fmt.Errorf("encoding the %s request: %w", path, err))
	}

	// The host is a name that resolves nowhere. What carries the request is the
	// transport's DialContext, which opens a stream on this worker's tunnel;
	// the host exists because an http.Request needs one, and it names the node
	// so a log line is diagnosable. See WorkerHTTPHost.
	url := "http://" + WorkerHTTPHost(nodeID, "") + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, controlFailure(ctx, nodeID, fmt.Errorf("building the %s request: %w", path, err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, controlFailure(ctx, nodeID, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusNoContent:
		return resp, nil
	case resp.StatusCode == http.StatusNotFound:
		// The worker answered, about ITSELF rather than about a backend: it is
		// older than this frontend and serves no such verb. The catch-all under
		// the control prefix is what makes this distinguishable from a proxy's
		// bare 404.
		_ = resp.Body.Close()
		return nil, fmt.Errorf("control rpc %s to node %q: %w", path, nodeID, ErrWorkerControlUnsupported)
	default:
		// A verb's own failure arrives as 200 with Error set, so a non-2xx is
		// the worker failing to serve the request rather than answering it: a
		// body it could not read, a method it refuses, a handler that panicked.
		// None of those is evidence about a backend.
		detail := readErrorBody(resp)
		_ = resp.Body.Close()
		return nil, controlFailure(ctx, nodeID, fmt.Errorf("the %s verb answered HTTP %d: %s", path, resp.StatusCode, detail))
	}
}

// maxControlErrorBodyBytes bounds how much of a non-2xx body reaches a log
// line. The body is written by the worker and lands in this frontend's logs, so
// it is bounded for the same reason the worker bounds the path it echoes.
const maxControlErrorBodyBytes = 512

// readErrorBody reads the diagnostic text off a non-2xx control response.
func readErrorBody(resp *http.Response) string {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxControlErrorBodyBytes))
	if err != nil {
		return "<the body could not be read>"
	}
	return string(bytes.TrimSpace(raw))
}

// controlFailure maps a control RPC's failure onto exactly one of the four
// conditions this system must never confuse, and it is the only place that
// mapping is made.
//
// The rule it enforces, stated as the code enforces it rather than as advice: a
// WORKER'S ANSWER passes through unwrapped so cluster.IsWorkerAnswer still sees
// it and a reap guard may act on it, and EVERYTHING ELSE is wrapped in
// ErrWorkerUnroutable so nothing can. There is no third branch, because a third
// branch is how the eight collapses on this branch happened: each was a place
// that decided for itself which errors were evidence.
//
// The caller's spent budget is checked FIRST, and that ordering is the same one
// cluster.WorkerDialer.handshake takes for the same reason. A worker's refusal
// that arrives in the instant the deadline expires would otherwise be reported
// as the worker's non-transient verdict and reap a row, and nothing orders the
// two timers: an expiry is never evidence about a backend, so it is answered as
// the caller's own timeout rather than as an answer.
func controlFailure(ctx context.Context, nodeID string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("control rpc to node %q: %w: %w", nodeID, ErrWorkerUnroutable, ctxErr)
	}
	if cluster.IsWorkerAnswer(err) {
		return err
	}
	return fmt.Errorf("control rpc to node %q: %w: %w", nodeID, ErrWorkerUnroutable, err)
}
