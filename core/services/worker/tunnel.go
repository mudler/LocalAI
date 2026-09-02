package worker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// The worker end of the tunnel.
//
// The worker DIALS OUT and never listens. It holds one WebSocket to the
// frontend load balancer, multiplexed with yamux, and every request the
// frontend makes of this worker arrives as a stream inside it. That is the
// whole point: a worker behind NAT, in another cluster or on a laptop needs no
// inbound port and no reachable address.
//
// This side is the yamux CLIENT and it only ACCEPTS streams; the frontend is
// the server and it only opens them. The frontend asks, the worker answers.
// Nothing here opens a stream, and a stream this side opened would park on the
// frontend's accept backlog, which accepts none.

const (
	// tunnelBackoffBase is the shortest wait between reconnects, before jitter.
	tunnelBackoffBase = 500 * time.Millisecond

	// tunnelBackoffMax is the ceiling on that wait.
	//
	// The ceiling is the interesting half. Without one, a worker that sits
	// through a long frontend outage backs off into hours and does not come
	// back for a long time after the frontend does; with one, the worst case
	// for rejoining is bounded by this. The floor and the jitter are what stop
	// a fleet of workers from turning a rolling restart into a retry storm
	// against the first replica back up.
	tunnelBackoffMax = 30 * time.Second

	// tunnelHealthyAfter is how long a session must last before the backoff is
	// allowed back to its floor.
	//
	// Resetting on CONNECT rather than on a session that lasted is the classic
	// way to build a reconnect storm that looks like a backoff: during a
	// rolling restart a replica accepts the dial and dies moments later, so
	// every attempt "succeeds" and every wait is the floor. This is set to the
	// yamux keepalive interval, which is the shortest interval over which a
	// session that is merely up can be told from one that is working.
	tunnelHealthyAfter = 30 * time.Second

	// tunnelHandshakeTimeout bounds the WebSocket upgrade, matching the peer
	// link's.
	tunnelHandshakeTimeout = 10 * time.Second

	// tunnelHeaderTimeout bounds how long a stream may go without sending the
	// request frame that says what it is for. It is generous because it bounds
	// only the frontend's own framing, which it writes immediately after
	// opening the stream; it is present because without it a stream that sends
	// nothing holds a goroutine and one of the session's stream slots for as
	// long as the tunnel lives.
	tunnelHeaderTimeout = 15 * time.Second
)

// LocalService opens a connection to one service running on this worker.
//
// target is the tag-specific argument from the stream's request frame, and the
// service decides what it will accept: the frontend naming an address does not
// oblige the worker to dial it. See loopbackService, which is what the worker
// actually installs.
type LocalService func(ctx context.Context, target string) (net.Conn, error)

// TunnelConfig configures the tunnel a worker holds to the frontend.
type TunnelConfig struct {
	// FrontendURL is the same value the worker registers against
	// (LOCALAI_REGISTER_TO). Its scheme is mapped to ws/wss here.
	FrontendURL string

	// NodeID is the identity registration assigned this worker.
	NodeID string

	// Token supplies the node's own tunnel credential.
	//
	// A function and not a string, and that is load-bearing rather than
	// stylistic. The credential is re-minted on every registration, so a client
	// that captured one at startup would keep presenting a value the frontend
	// stopped accepting the moment anything re-registered this worker, and
	// would lock itself out with no way back. It is called once per DIAL.
	Token func() string

	// Services routes an accepted stream by the tag in its request frame. A tag
	// with no entry here is refused; see Tunnel.accept.
	Services map[string]LocalService

	// Seams the specs replace. They are unexported so they are not part of the
	// package's API: a caller cannot reach them, and the internal test file can.
	sleep         func(ctx context.Context, d time.Duration) error
	now           func() time.Time
	headerTimeout time.Duration
}

// Tunnel is a running worker tunnel: one goroutine holding one session at a
// time, reconnecting when it dies, until Close.
type Tunnel struct {
	endpoint string
	nodeID   string
	token    func() string
	services map[string]LocalService
	dialer   *websocket.Dialer

	headerTimeout time.Duration
	sleep         func(ctx context.Context, d time.Duration) error
	// now measures how long a session lasted, and nothing else. Deadlines are
	// taken from time.Now directly: a spec that fakes this clock to exercise
	// the backoff must not thereby move every I/O deadline in the package.
	now func() time.Time

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// StartTunnel dials the frontend and holds the tunnel until ctx is cancelled or
// Close is called.
//
// The returned error is about this CONFIGURATION, never about the frontend. A
// frontend that is down, that has not been upgraded, or that refuses the
// credential is not a reason for a worker to fail to start: it retries, with
// backoff, in the background. Failing to start on a dial would make a frontend
// restart into a fleet-wide worker outage.
func StartTunnel(ctx context.Context, cfg TunnelConfig) (*Tunnel, error) {
	if cfg.NodeID == "" {
		return nil, errors.New("starting the worker tunnel: no node id")
	}
	if cfg.Token == nil {
		return nil, errors.New("starting the worker tunnel: no credential source")
	}
	endpoint, err := tunnelEndpoint(cfg.FrontendURL, cfg.NodeID)
	if err != nil {
		return nil, err
	}

	// Copied so the tunnel's routing table cannot change under the accept loop
	// after it has started.
	services := make(map[string]LocalService, len(cfg.Services))
	for tag, svc := range cfg.Services {
		services[tag] = svc
	}

	t := &Tunnel{
		endpoint: endpoint,
		nodeID:   cfg.NodeID,
		token:    cfg.Token,
		services: services,
		dialer: &websocket.Dialer{
			HandshakeTimeout: tunnelHandshakeTimeout,
			// A worker reaches its frontend over the public internet in the
			// deployments this exists for, so unlike the replica-to-replica
			// peer link this DOES honour the environment's proxy settings.
			Proxy: http.ProxyFromEnvironment,
		},
		headerTimeout: cmp.Or(cfg.headerTimeout, tunnelHeaderTimeout),
		sleep:         cfg.sleep,
		now:           cfg.now,
		done:          make(chan struct{}),
	}
	if t.sleep == nil {
		t.sleep = tunnelSleep
	}
	if t.now == nil {
		t.now = time.Now
	}

	loopCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	go func() {
		defer close(t.done)
		t.run(loopCtx)
	}()
	return t, nil
}

// Close stops the tunnel and waits for its loop to finish. It is idempotent.
func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		t.cancel()
		<-t.done
	})
	return nil
}

// run holds one session at a time, reconnecting with bounded backoff.
func (t *Tunnel) run(ctx context.Context) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		start := t.now()
		err := t.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}

		// A session that LASTED is the only evidence the frontend is healthy.
		// See tunnelHealthyAfter for why "we connected" is not.
		if t.now().Sub(start) >= tunnelHealthyAfter {
			attempt = 0
		}
		attempt++

		delay := tunnelBackoffDelay(attempt)
		t.logSessionEnded(err, attempt, delay)
		if err := t.sleep(ctx, delay); err != nil {
			return
		}
	}
}

// connectAndServe dials, serves streams until the session ends, and leaves
// nothing running behind it.
func (t *Tunnel) connectAndServe(ctx context.Context) error {
	ws, err := t.dial(ctx)
	if err != nil {
		return err
	}

	sess, err := yamux.Client(cluster.WebsocketConn(ws), nil, nil)
	if err != nil {
		_ = ws.Close()
		return fmt.Errorf("starting the worker tunnel session: %w", err)
	}
	xlog.Info("Worker tunnel established", "node", t.nodeID, "frontend", t.endpoint)

	// Streams are served under a context of the SESSION's, not the loop's. A
	// stream goroutine parked in a local dial would otherwise outlive the
	// session it belongs to and hold the reconnect below behind it.
	sessCtx, endSession := context.WithCancel(ctx)

	// AcceptStream takes no context, so something else has to break it when the
	// worker is shutting down; closing the session is that something.
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		select {
		case <-sessCtx.Done():
			_ = sess.Close()
		case <-sess.CloseChan():
		}
	}()

	var streams sync.WaitGroup
	serveErr := t.serve(sessCtx, sess, &streams)

	endSession()
	_ = sess.Close()
	<-watchdogDone
	// Closing the session unblocks every stream goroutine: Session.close walks
	// its stream table calling forceClose on each (session.go:334-338), and
	// forceClose puts both directions in halfReset and calls notifyWaiting
	// (stream.go:371-388), which wakes a parked Read and fails a parked Write.
	// Waiting here is what keeps a reconnect from overlapping the streams of
	// the session it replaced.
	streams.Wait()
	return serveErr
}

// serve accepts streams until the session ends.
//
// One goroutine per stream, and an error from a stream never reaches this loop.
// A single malformed or unroutable request must not cost this worker every
// other request in flight on the same session.
func (t *Tunnel) serve(ctx context.Context, sess *yamux.Session, streams *sync.WaitGroup) error {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return err
		}
		streams.Add(1)
		go func() {
			defer streams.Done()
			t.handleStream(ctx, stream)
		}()
	}
}

// handleStream reads one stream's request frame and either splices it to a
// local service or refuses it.
func (t *Tunnel) handleStream(ctx context.Context, stream net.Conn) {
	// A panic under one stream must not take the worker down, and here that is
	// not a figure of speech: nothing supervises this goroutine, so an
	// unrecovered panic ends the PROCESS, which ends the session and every
	// other stream on it. Unlike the frontend's handler next door this does not
	// re-panic, because there is no recovery middleware above it to report the
	// panic; re-panicking would only be the crash.
	//
	// It covers what runs ON THIS goroutine: reading the request frame, the
	// route lookup, and the local service's dial, which is the one of the three
	// that runs caller-supplied code. It does NOT cover a panic inside Splice's
	// own copy goroutines, which no recover here can reach.
	defer func() {
		if r := recover(); r != nil {
			xlog.Error("Panic while serving a worker tunnel stream", "node", t.nodeID, "panic", r)
			_ = stream.Close()
		}
	}()

	local, ok := t.accept(ctx, stream)
	if !ok {
		// accept has already answered and closed the stream.
		return
	}

	// Splice owns closing both ends from here.
	if err := cluster.Splice(stream, local); err != nil {
		xlog.Debug("worker tunnel stream ended with an error", "node", t.nodeID, "error", err)
	}
}

// accept reads the request frame and resolves it to a local connection. The
// second result is false when the stream was refused, in which case the refusal
// has been sent and the stream closed.
func (t *Tunnel) accept(ctx context.Context, stream net.Conn) (net.Conn, bool) {
	// Deliberately time.Now and not t.now: this is an I/O deadline, and the
	// clock seam exists only to measure how long a session lasted.
	if err := stream.SetReadDeadline(time.Now().Add(t.headerTimeout)); err != nil {
		// NotServed and not TargetUnavailable: this is a fact about the STREAM,
		// which would not take a deadline, and no local service has been named
		// yet, let alone dialled. Reporting it as an unreachable target would
		// tell the frontend a backend it has not asked about is gone.
		t.refuse(stream, fmt.Errorf("%w: arming the request deadline: %v", cluster.ErrStreamNotServed, err))
		return nil, false
	}

	tag, target, err := cluster.ReadStreamRequest(stream)
	if err != nil {
		// The two causes are SEPARATED here, and merging them was a real
		// defect. A malformed frame is the frontend's own bug and does not
		// clear on its own, so it stays a verdict the frontend acts on. The
		// deadline above expiring is a frame that has not ARRIVED yet, which
		// clears the moment the link drains; on the relay path the worker's
		// timer starts when the OWNING replica opens the stream, while the
		// frame is written by the DIALLING replica only after the relay's
		// acceptance has travelled back to it, so a whole peer-link round trip
		// runs inside this window, on a link this design deliberately loads
		// with multi-gigabyte artifacts. Reported as a malformed request it
		// became reaping evidence, and for a long-deadline caller that is a
		// model evicted across the fleet by nothing but congestion.
		if isReadTimeout(err) {
			t.refuse(stream, fmt.Errorf("%w: %v", cluster.ErrStreamNotServed, err))
			return nil, false
		}
		t.refuse(stream, fmt.Errorf("%w: %v", cluster.ErrStreamRequestInvalid, err))
		return nil, false
	}

	svc, known := t.services[tag]
	if !known {
		// A ROUTING fact about this worker, and it is reported as itself. A
		// frontend that reads this knows a retry is pointless until the worker
		// is upgraded, which is not what it should conclude from the
		// unavailable below.
		t.refuse(stream, fmt.Errorf("%w: %q", cluster.ErrStreamTagUnknown, tag))
		return nil, false
	}

	// Cleared before the local dial rather than after the reply: everything
	// past the request frame belongs to the tunnelled protocol, which brings
	// its own deadlines, and one left armed here would abort a long inference
	// stream in the middle.
	if err := stream.SetReadDeadline(time.Time{}); err != nil {
		// NotServed for the same reason as arming it: the stream is what
		// failed, and this worker has said nothing about the target.
		t.refuse(stream, fmt.Errorf("%w: clearing the request deadline: %v", cluster.ErrStreamNotServed, err))
		return nil, false
	}

	local, err := svc(ctx, target)
	if err != nil {
		t.refuse(stream, classifyServiceFailure(err))
		return nil, false
	}

	if err := cluster.WriteStreamAccepted(stream); err != nil {
		// The frontend never learns the stream was accepted, so it cannot be
		// used; closing the local connection here is what stops an accepted
		// backend connection leaking per failed reply.
		xlog.Debug("worker tunnel could not accept a stream", "node", t.nodeID, "error", err)
		_ = local.Close()
		_ = stream.Close()
		return nil, false
	}
	return local, true
}

// classifyServiceFailure decides which refusal a local service's error is.
//
// A service that has ALREADY classified its own failure keeps that
// classification. loopbackService does, and the distinction is not cosmetic: a
// target outside this worker's backend port range is a request this worker will
// never serve, while a backend that is not listening yet is a condition that
// clears on its own. Reporting the first as the second tells a frontend to
// retry something that can never work; reporting the second as the first makes
// it give up on a backend that is merely starting.
//
// The default is TargetUnavailable and stays that way, which is the deliberate
// half of this function now that the frontend acts on that code. A dial to the
// named process that came back with anything is the closest thing to evidence
// this worker can produce, and the direction of a mis-classification decides
// which mistake is made: defaulting the other way would mean a genuinely dead
// backend whose errno nobody enumerated becomes a permanently unreapable row,
// which is the exact defect this phase spent a round removing. So the
// exemptions below are a DENY-list of causes that are provably not about the
// target, not an allow-list of causes that are.
//
// The two exempted are the caller's context ending and a read/write deadline
// firing. Neither is the target answering: the context here is the SESSION's,
// so it ends when this worker's tunnel is torn down, and a deadline is this
// process's own timer. Both clear on a reconnect. They are exempted rather
// than argued away as unreachable because "unreachable" was the argument that
// made the request-frame merge look safe.
//
// It must never become the unknown-tag refusal either: a tag this worker serves
// does not stop being served because one dial failed.
func classifyServiceFailure(err error) error {
	if errors.Is(err, cluster.ErrStreamRequestInvalid) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isReadTimeout(err) {
		return fmt.Errorf("%w: %v", cluster.ErrStreamNotServed, err)
	}
	return fmt.Errorf("%w: %v", cluster.ErrStreamTargetUnavailable, err)
}

// isReadTimeout reports whether err is an I/O deadline firing rather than a
// peer saying anything.
//
// net.Error's Timeout is asked as well as os.ErrDeadlineExceeded because the
// two are not the same set: a yamux stream returns its own timeout value from
// a Read whose deadline expired, and a net.OpError over a socket returns
// os.ErrDeadlineExceeded. Missing either would put a timeout back on the
// verdict path, which is the defect this predicate exists to keep closed.
func isReadTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// refuse reports why a stream will not be served and then ENDS it.
//
// The close is the part that matters and it is not optional. A worker that says
// why and leaves the stream open has parked the frontend on a request that will
// never be answered, which reads as a slow worker rather than a refused
// request, and a deadline on the far side cannot tell those apart. The reply is
// what makes the refusal legible; the close is what makes it prompt.
//
// The reply is therefore best-effort and the close is not: a reply that could
// not be written still gets the stream closed.
func (t *Tunnel) refuse(stream net.Conn, reason error) {
	if err := cluster.WriteStreamRefusal(stream, reason); err != nil {
		xlog.Debug("worker tunnel could not report why it refused a stream", "node", t.nodeID, "error", err)
	}
	_ = stream.Close()
	xlog.Debug("worker tunnel refused a stream", "node", t.nodeID, "reason", reason)
}

// dial opens the WebSocket and returns it.
func (t *Tunnel) dial(ctx context.Context) (*websocket.Conn, error) {
	// Read HERE, once per dial. See TunnelConfig.Token.
	token := t.token()
	if token == "" {
		// Not a dial that fails with "unauthorized": this worker has no
		// credential yet, which is a different condition from the frontend
		// rejecting one, and an operator reading "unauthorized" would go
		// looking for a token mismatch that does not exist.
		return nil, errors.New("dialling the worker tunnel: this node has no tunnel credential yet")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	ws, resp, err := t.dialer.DialContext(ctx, t.endpoint, header)
	if err != nil {
		if resp != nil {
			// gorilla reports every non-101 as the same ErrBadHandshake, so
			// without the status a 401, a 403 and a 503 are one log line.
			defer func() { _ = resp.Body.Close() }()
			return nil, &tunnelDialError{status: resp.StatusCode, cause: err}
		}
		return nil, fmt.Errorf("dialling the worker tunnel: %w", err)
	}
	return ws, nil
}

// tunnelDialError carries the HTTP status a refused dial came back with, so the
// four refusals the frontend can give are not logged as one.
type tunnelDialError struct {
	status int
	cause  error
}

func (e *tunnelDialError) Error() string {
	return fmt.Sprintf("dialling the worker tunnel: frontend answered %d: %v", e.status, e.cause)
}

func (e *tunnelDialError) Unwrap() error { return e.cause }

// logSessionEnded says why the tunnel is reconnecting, at a level that matches
// what the operator can do about it.
//
// The distinctions are the point rather than decoration. "Awaiting approval"
// and "your token is wrong" and "this frontend does not do tunnels" send an
// operator to three different places, and a worker retries all three the same
// way: none of them is a reason to stop, because a re-registration or an admin
// action fixes each without restarting the worker.
func (t *Tunnel) logSessionEnded(err error, attempt int, delay time.Duration) {
	if err == nil {
		xlog.Info("Worker tunnel closed, reconnecting", "node", t.nodeID, "attempt", attempt, "retry_in", delay)
		return
	}

	var dialErr *tunnelDialError
	if errors.As(err, &dialErr) {
		switch dialErr.status {
		case http.StatusUnauthorized:
			// Named causes, because this worker cannot recover from either on
			// its own and the two need different actions. It has no inbound
			// listener and no advertised address, so a tunnel it cannot open is
			// a worker nothing can reach: this is an outage, not a warning about
			// a degraded path.
			xlog.Warn("Frontend rejected this worker's tunnel credential, so nothing can reach this worker; "+
				"either another worker registered under this node name and rotated the credential (check LOCALAI_NODE_NAME is unique), "+
				"or the frontend's record of this node was replaced. Restarting this worker re-registers and mints a fresh credential",
				"node", t.nodeID, "retry_in", delay)
		case http.StatusForbidden:
			xlog.Info("Worker tunnel refused: this node is awaiting admin approval",
				"node", t.nodeID, "retry_in", delay)
		case http.StatusNotFound:
			xlog.Debug("frontend does not serve worker tunnels, so it predates them",
				"node", t.nodeID, "retry_in", delay)
		case http.StatusServiceUnavailable:
			xlog.Debug("frontend is not running in distributed mode, so it holds no worker tunnels",
				"node", t.nodeID, "retry_in", delay)
		default:
			xlog.Warn("Worker tunnel dial refused", "node", t.nodeID, "status", dialErr.status,
				"attempt", attempt, "retry_in", delay, "error", err)
		}
		return
	}
	xlog.Warn("Worker tunnel ended, reconnecting", "node", t.nodeID, "attempt", attempt, "retry_in", delay, "error", err)
}

// tunnelBackoffDelay returns how long to wait before reconnect attempt n.
//
// Equal jitter: half the delay is fixed and half is drawn. Full jitter, which
// draws over the whole interval, can produce a near-zero wait, and a worker
// that can draw a near-zero wait can spin; keeping a floor means no single
// worker ever does, while the drawn half is what stops a fleet that all lost
// the same replica from resynchronising onto the same instant.
func tunnelBackoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := tunnelBackoffMax
	// The shift is guarded twice. The bound on attempt keeps the shift itself
	// defined, and the positivity check catches the overflow that would
	// otherwise turn a long outage into a NEGATIVE delay, which is a tight loop
	// wearing a backoff's costume.
	if attempt <= 40 {
		if scaled := tunnelBackoffBase << (attempt - 1); scaled > 0 && scaled < tunnelBackoffMax {
			d = scaled
		}
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// tunnelSleep waits for d, or returns early when ctx is cancelled.
func tunnelSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// tunnelEndpoint turns the frontend URL a worker registers against into the
// WebSocket URL it dials its tunnel on.
func tunnelEndpoint(frontendURL, nodeID string) (string, error) {
	if frontendURL == "" {
		return "", errors.New("starting the worker tunnel: no frontend URL")
	}
	u, err := url.Parse(frontendURL)
	if err != nil {
		return "", fmt.Errorf("starting the worker tunnel: parsing frontend URL %q: %w", frontendURL, err)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("starting the worker tunnel: frontend URL %q has scheme %q, want http or https", frontendURL, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("starting the worker tunnel: frontend URL %q has no host", frontendURL)
	}
	// Appended rather than assigned, so a frontend served under a path prefix
	// keeps it. Registration builds its URLs the same way.
	u.Path = strings.TrimRight(u.Path, "/") + cluster.ConnectPath
	u.RawQuery = url.Values{"id": []string{nodeID}}.Encode()
	return u.String(), nil
}

// loopbackService routes a tagged stream to a process listening on this
// worker's own loopback interface.
//
// The HOST the frontend names is discarded and only the port is used, which is
// deliberate and is the security property this function exists for. A tunnel
// terminates inside the worker process, so a stream arriving on it can reach
// anything the worker can reach; without this, whoever holds the frontend end
// could make every worker in the fleet dial arbitrary hosts on its private
// network, turning the tunnel into a proxy into the worker's LAN. Discarding
// the host reduces the reachable set to this machine.
//
// It also happens to be what makes the tunnel work BEFORE the workers stop
// advertising themselves: today the frontend names the address the worker
// registered, which is a routable one, and after that change it will name a
// loopback one. Both resolve to the same place here.
//
// The port range is the one the worker's own port allocator hands to backend
// processes, so a stream cannot be pointed at some unrelated service that
// happens to be listening on this host. It is only as tight as the allocator's
// range, which by default runs to 65535; a deployment that wants it narrow sets
// LOCALAI_GRPC_MAX_PORT, which narrows both at once.
//
// Note the SHAPE, not only the checks. Nothing derived from the wire reaches
// the dialler: the address is built from the loopbackHost constant and from
// strconv.Itoa of an int this function validated, so `target` itself has no
// path to DialContext at all. Relaxing this into an arbitrary-host dialler
// therefore takes ADDING a data flow rather than deleting a check, which is the
// difference between a guard and a property. It has specs either way; the shape
// is what stops a plausible refactor from quietly restoring the hole.
func loopbackService(minPort, maxPort int) LocalService {
	return func(ctx context.Context, target string) (net.Conn, error) {
		_, portStr, err := net.SplitHostPort(target)
		if err != nil {
			return nil, fmt.Errorf("%w: routing a tunnel stream: %q is not a host:port: %v",
				cluster.ErrStreamRequestInvalid, target, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("%w: routing a tunnel stream: %q has no numeric port: %v",
				cluster.ErrStreamRequestInvalid, target, err)
		}
		if port < minPort || port > maxPort {
			// Invalid rather than unavailable: no retry can bring a port
			// outside this worker's own allocator range into it.
			return nil, fmt.Errorf("%w: routing a tunnel stream: port %d is outside this worker's backend range [%d, %d]",
				cluster.ErrStreamRequestInvalid, port, minPort, maxPort)
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(port)))
	}
}

// loopbackHost is the host every stream the FRONTEND CAN STEER is dialled on.
//
// It is a constant so that "a stream cannot choose where the worker dials" is a
// fact about the code rather than a claim about its inputs: the grpc tag builds
// its address from this and a port it validated, and nothing derived from the
// wire reaches the dialler.
//
// It is NOT the only host this file ever dials, and the difference is worth
// stating exactly rather than summarising, because the whole argument about
// what a stream can reach rests on knowing which hosts are reachable, and an
// overstatement here is what would let a future reader conclude the constant
// alone is doing the work.
//
// fixedService dials whatever address it was constructed with. Run constructs
// it from this worker's own LOCALAI_HTTP_ADDR, which an operator may set to a
// routable address; loopbackAddr only rewrites a WILDCARD bind, and leaves an
// explicit host alone on purpose, because a server bound to one address is not
// reachable on another. So the http tag can dial a non-loopback host. That host
// is one the OPERATOR configured for this worker's own server, never one a
// stream names: fixedService ignores its target entirely. The property the
// design needs is that the frontend cannot steer the dial, and that holds for
// both tags.
const loopbackHost = "127.0.0.1"

// tunnelServices builds the routing table the worker installs on its tunnel.
//
// It exists as its own function so the table can be specced. The table is the
// security boundary of this whole feature, and building it inline in Run left
// it reachable only by starting a worker, which meant it was covered by nothing
// and an arbitrary-host regression passed the entire suite.
func tunnelServices(cfg *Config, httpBindAddr string) map[string]LocalService {
	basePort := cfg.effectiveBasePort()
	return map[string]LocalService{
		// The frontend names a backend process by its port; the worker decides
		// that only its own loopback, and only within its own backend port
		// range, is reachable through it.
		cluster.StreamTagGRPC: loopbackService(basePort, cfg.effectiveMaxPort(basePort)),
		cluster.StreamTagHTTP: fixedService(loopbackAddr(httpBindAddr)),
	}
}

// fixedService routes a tagged stream to one address on this worker, ignoring
// whatever the frontend named.
//
// There is exactly one HTTP server per worker and only the worker knows where
// it bound, so the frontend has nothing useful to say about the target and is
// not given the chance to say it.
func fixedService(addr string) LocalService {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

// loopbackAddr rewrites a bind address into one that reaches the same listener
// from inside this process.
//
// A server bound to 0.0.0.0 is reachable on loopback, but dialling 0.0.0.0 is
// only accidentally equivalent to dialling localhost and is not on every
// platform, so the wildcard is replaced rather than dialled.
func loopbackAddr(bindAddr string) string {
	host, port, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return bindAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return net.JoinHostPort(loopbackHost, port)
	}
	return bindAddr
}
