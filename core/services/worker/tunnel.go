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
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/tunnel"
)

// The worker end of the tunnel.
//
// The worker dials out and never listens. It holds a websocket to a frontend and
// a multiplexed session on it, and every request that the frontend makes of this
// worker arrives as a stream in that session. A worker behind NAT, in another
// cluster or on a laptop needs no inbound port and no address that others can
// reach.
//
// A worker holds two sessions. The inference lane carries model calls, control
// and health. The bulk lane carries file transfers, so that a large transfer
// does not delay a small call. The bulk lane exists only while the inference
// lane exists: the frontend accepts it only on the replica that holds the
// inference lane, and the worker closes it with the inference lane.
//
// This side is the yamux client and it only accepts streams. The frontend is the
// server and it only opens them. The frontend asks and the worker answers.

const (
	// tunnelBackoffBase is the shortest wait between two attempts to connect,
	// before the jitter.
	tunnelBackoffBase = 500 * time.Millisecond

	// tunnelBackoffMax is the longest wait. Without a limit, a worker that sits
	// through a long outage of the frontend waits for hours and does not come
	// back for a long time after the frontend does. The jitter and the floor stop
	// a fleet of workers from turning a rolling restart into a storm of retries
	// against the first replica that is back.
	tunnelBackoffMax = 30 * time.Second

	// tunnelHealthyAfter is how long a session must last before the wait goes
	// back to its floor. If the wait went back at once on connect, a replica that
	// accepts the dial and dies a moment later would give a worker that retries
	// at the floor for ever. The value is the keepalive interval of yamux, which
	// is the shortest time over which a session that is only up can be told from
	// one that works.
	tunnelHealthyAfter = 30 * time.Second

	// tunnelHandshakeTimeout bounds the websocket upgrade.
	tunnelHandshakeTimeout = 10 * time.Second

	// tunnelWrongReplicaDelay is the base of the wait after the answer 409 on the
	// bulk lane. The answer means that the load balancer sent the dial to a
	// replica that does not hold the inference lane. Another dial can land on the
	// right one, so the wait is short.
	tunnelWrongReplicaDelay = 250 * time.Millisecond

	// tunnelHeaderTimeout bounds how long a stream may take to send its request
	// frame. Without a bound, a stream that sends nothing would hold a goroutine
	// and a slot of the session for as long as the tunnel lives.
	//
	// The bound is long on purpose. On the relay path the timer starts when the
	// owning replica opens the stream, and the dialling replica writes the frame
	// only after the acceptance of the relay has come back to it. A round trip
	// of a link between replicas, which carries artifacts of many gigabytes
	// beside token streams, is inside this time. An expiry says nothing about
	// the frontend, so it is refused with tunnel.ErrStreamNotServed. See accept.
	tunnelHeaderTimeout = 15 * time.Second
)

// LocalService opens a connection to a service that runs on this worker.
//
// target is the argument of the tag in the request frame of the stream. The
// service decides what it accepts: a frontend that names an address does not
// oblige the worker to dial it. See loopbackService.
type LocalService func(ctx context.Context, target string) (net.Conn, error)

// TunnelConfig configures the tunnel that a worker holds to a frontend.
type TunnelConfig struct {
	// FrontendURL is the URL that the worker registers with. The tunnel changes
	// its scheme to ws or wss.
	FrontendURL string

	// NodeID is the identity that registration gave to this worker.
	NodeID string

	// Token returns the own tunnel credential of the node. The credential is new
	// after every registration, so a client that kept the value from its start
	// would present a value that the frontend no longer accepts, as soon as
	// anything registered this worker again. Token is called once for every dial.
	Token func() string

	// Services routes a stream by the tag in its request frame. A tag with no
	// entry is refused. See accept.
	Services map[string]LocalService

	// Reauthorize registers the node again, so that Token returns a credential
	// that the frontend accepts. It is called after a dial that the frontend
	// refused with 401 or 403, and at most once for each wait of the backoff.
	// Another process that registers under the same node name, or a frontend that
	// lost its record of the node, makes the credential of this worker useless,
	// and only a registration mints a new one. A nil Reauthorize leaves the
	// worker to retry the credential it has.
	Reauthorize func(ctx context.Context) error

	// The fields below are for the specs. They are not exported, so that a caller
	// cannot reach them.
	sleep         func(ctx context.Context, d time.Duration) error
	now           func() time.Time
	headerTimeout time.Duration
	// noBulk stops the worker from dialling the bulk lane. A spec that measures
	// the waits of the inference lane needs the waits of the other lane out of
	// its way.
	noBulk bool
}

// Tunnel is a running worker tunnel. One goroutine holds the inference session
// and connects it again when it ends, until Close.
type Tunnel struct {
	endpoint string
	nodeID   string
	token    func() string
	services map[string]LocalService
	dialer   *websocket.Dialer

	reauthorize func(ctx context.Context) error

	headerTimeout time.Duration
	noBulk        bool
	sleep         func(ctx context.Context, d time.Duration) error
	// now measures how long a session lasted and nothing else. Deadlines use
	// time.Now. A spec that fakes this clock to test the wait must not move
	// every deadline of the package.
	now func() time.Time

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once

	// mu guards the sessions. The loops write them. Connected and
	// BulkConnected read them, and on a running worker the reader is the
	// handler of /readyz.
	mu        sync.Mutex
	inference *tunnel.Session
	bulk      *tunnel.Session
}

// Connected reports whether the tunnel holds an inference session that has not
// ended.
//
// It is false between two sessions and while the first dial is in progress. That
// is a statement about reachability and nothing else. A worker that connects
// again after a restart of its frontend reports false and is still a registered,
// running node. Nothing may read it as the worker being gone.
func (t *Tunnel) Connected() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	sess := t.inference
	t.mu.Unlock()
	// A session that ended is the value of the field until the loop clears it.
	// The gap between the two is the time in which a probe must not answer 200.
	return sess != nil && !sess.IsClosed()
}

// BulkConnected reports whether the tunnel holds a bulk session that has not
// ended.
func (t *Tunnel) BulkConnected() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	sess := t.bulk
	t.mu.Unlock()
	return sess != nil && !sess.IsClosed()
}

func (t *Tunnel) setSession(lane tunnel.Lane, sess *tunnel.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if lane == tunnel.LaneBulk {
		t.bulk = sess
		return
	}
	t.inference = sess
}

// StartTunnel dials the frontend and holds the tunnel until ctx ends or Close is
// called.
//
// The error is about the configuration and never about the frontend. A frontend
// that is down, that has not been upgraded or that refuses the credential is no
// reason for a worker not to start: it tries again in the background, with a
// wait that grows. If a failed dial made the start fail, the restart of a
// frontend would become an outage of the whole fleet of workers.
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

	// Copied, so that the routing table cannot change under the accept loop.
	services := make(map[string]LocalService, len(cfg.Services))
	for tag, svc := range cfg.Services {
		services[tag] = svc
	}

	dialer := tunnel.NewDialer(tunnelHandshakeTimeout)
	// A worker reaches its frontend over the internet in the deployments that
	// this is for, so it honours the proxy settings of the environment.
	dialer.Proxy = http.ProxyFromEnvironment

	t := &Tunnel{
		endpoint:      endpoint,
		nodeID:        cfg.NodeID,
		token:         cfg.Token,
		services:      services,
		dialer:        dialer,
		reauthorize:   cfg.Reauthorize,
		headerTimeout: cmp.Or(cfg.headerTimeout, tunnelHeaderTimeout),
		noBulk:        cfg.noBulk,
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

// Close stops the tunnel and waits for its loop to end. It is idempotent.
func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		t.cancel()
		<-t.done
	})
	return nil
}

// run holds one inference session at a time and connects again with a wait that
// is bounded.
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

		// A session that lasted is the only sign that the frontend is well. See
		// tunnelHealthyAfter for why a connect is not.
		if t.now().Sub(start) >= tunnelHealthyAfter {
			attempt = 0
		}
		attempt++

		delay := tunnelBackoffDelay(attempt)
		t.logSessionEnded(err, attempt, delay)
		t.reauthorizeAfter(ctx, err)
		if err := t.sleep(ctx, delay); err != nil {
			return
		}
	}
}

// reauthorizeAfter registers the node again when the frontend refused the
// credential of the dial.
//
// It runs once for each pass of the loop, and the loop waits with a growing
// backoff after it, so a frontend that keeps refusing is asked no more often
// than the backoff allows. Two workers that share a node name rotate each
// other's credential on every registration; the backoff bounds that contest to
// one registration for each wait and does not end it. The operator fixes the
// duplicate name.
func (t *Tunnel) reauthorizeAfter(ctx context.Context, err error) {
	if t.reauthorize == nil || !refusedCredential(err) {
		return
	}
	if rerr := t.reauthorize(ctx); rerr != nil {
		if ctx.Err() == nil {
			xlog.Warn("Registering again after the frontend refused the tunnel credential failed", "node", t.nodeID, "error", rerr)
		}
		return
	}
	xlog.Info("Registered again after the frontend refused the tunnel credential", "node", t.nodeID)
}

// refusedCredential reports whether the frontend answered a dial with 401 or
// 403. Both can be cured by a new registration: 401 is a credential that the
// frontend does not know, and 403 is a node whose record the registration
// updates.
func refusedCredential(err error) bool {
	var dialErr *tunnelDialError
	if !errors.As(err, &dialErr) {
		return false
	}
	return dialErr.status == http.StatusUnauthorized || dialErr.status == http.StatusForbidden
}

// connectAndServe dials the inference lane, serves streams until the session
// ends, and leaves nothing running behind it.
func (t *Tunnel) connectAndServe(ctx context.Context) error {
	sess, err := t.dial(ctx, tunnel.LaneInference)
	if err != nil {
		return err
	}
	xlog.Info("Worker tunnel established", "node", t.nodeID, "frontend", t.endpoint)

	// Published before the accept loop starts. Connected also checks IsClosed,
	// and that check is what keeps the answer right after the session ended: the
	// clear below runs after the streams that are in progress have ended, and the
	// probe must say "not ready" during that wait.
	t.setSession(tunnel.LaneInference, sess)
	defer t.setSession(tunnel.LaneInference, nil)

	// Streams run under the context of the session and not under the one of the
	// loop. A stream that waits for a local dial would outlive the session and
	// hold the next connect behind it.
	sessCtx, endSession := context.WithCancel(ctx)

	// AcceptStream takes no context, so closing the session is what stops it
	// when the worker shuts down.
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
	// The bulk lane lives as long as this inference session.
	if !t.noBulk {
		streams.Go(func() { t.runBulk(sessCtx, &streams) })
	}

	serveErr := t.serve(sessCtx, sess, &streams)

	endSession()
	_ = sess.Close()
	<-watchdogDone
	// Closing the session ends every stream goroutine: yamux closes the streams
	// of a session that closes, which wakes a read and fails a write. The wait
	// keeps a new connect from overlapping the streams of the session it
	// replaced.
	streams.Wait()
	return serveErr
}

// runBulk holds the bulk session for as long as ctx lives. It dials again when
// the session ends, and it does not stop the inference lane when it fails. A
// frontend that predates the bulk lane answers a dial to it as an inference
// dial, or refuses it. Either way the worker must still serve.
func (t *Tunnel) runBulk(ctx context.Context, streams *sync.WaitGroup) {
	attempt := 0
	for ctx.Err() == nil {
		start := t.now()
		sess, err := t.dial(ctx, tunnel.LaneBulk)
		var delay time.Duration
		switch {
		case err == nil:
			xlog.Info("Worker bulk tunnel established", "node", t.nodeID)
			t.setSession(tunnel.LaneBulk, sess)
			serveErr := t.serveUntilDone(ctx, sess, streams)
			t.setSession(tunnel.LaneBulk, nil)
			if ctx.Err() != nil {
				return
			}
			if t.now().Sub(start) >= tunnelHealthyAfter {
				attempt = 0
			}
			attempt++
			delay = tunnelBackoffDelay(attempt)
			xlog.Debug("worker bulk tunnel ended", "node", t.nodeID, "retry_in", delay, "error", serveErr)
		case isWrongReplica(err):
			// The dial landed on a replica that does not hold the inference
			// lane. Not a failure, and not an attempt for the wait.
			delay = wrongReplicaDelay()
			xlog.Debug("worker bulk tunnel dial reached a replica that does not hold the tunnel", "node", t.nodeID, "retry_in", delay)
		default:
			if ctx.Err() != nil {
				// The inference session ended during the dial. That is not a
				// failure of the bulk lane.
				return
			}
			attempt++
			delay = tunnelBackoffDelay(attempt)
			xlog.Debug("worker bulk tunnel dial failed", "node", t.nodeID, "retry_in", delay, "error", err)
		}
		if err := t.sleep(ctx, delay); err != nil {
			return
		}
	}
}

// serveUntilDone serves the streams of a session until it ends or ctx ends, and
// returns after the streams have ended.
func (t *Tunnel) serveUntilDone(ctx context.Context, sess *tunnel.Session, streams *sync.WaitGroup) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Close()
		case <-done:
		}
	}()
	err := t.serve(ctx, sess, streams)
	_ = sess.Close()
	return err
}

// serve accepts streams until the session ends.
//
// A goroutine serves each stream, and the error of a stream never reaches this
// loop. A request that is malformed or cannot be routed must not cost the worker
// the other requests of the session.
func (t *Tunnel) serve(ctx context.Context, sess *tunnel.Session, streams *sync.WaitGroup) error {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return err
		}
		streams.Go(func() { t.handleStream(ctx, stream) })
	}
}

// handleStream reads the request frame of a stream and then joins the stream to
// a local service, or refuses it.
func (t *Tunnel) handleStream(ctx context.Context, stream net.Conn) {
	// A panic under one stream must not take the worker down. Nothing supervises
	// this goroutine, so a panic that is not recovered ends the process, and with
	// it the session and every other stream. This does not panic again, because
	// no middleware above it would report the panic. It covers what runs in this
	// goroutine: the request frame, the lookup of the route, and the dial of the
	// local service. It does not cover the copy goroutines of Splice.
	defer func() {
		if r := recover(); r != nil {
			xlog.Error("Panic while serving a worker tunnel stream", "node", t.nodeID, "panic", r)
			_ = stream.Close()
		}
	}()

	local, ok := t.accept(ctx, stream)
	if !ok {
		// accept has answered and closed the stream.
		return
	}

	// Splice closes both ends.
	if err := tunnel.Splice(stream, local); err != nil {
		xlog.Debug("worker tunnel stream ended with an error", "node", t.nodeID, "error", err)
	}
}

// accept reads the request frame and resolves it to a local connection. The
// result is false when the stream was refused: the refusal was sent and the
// stream closed.
func (t *Tunnel) accept(ctx context.Context, stream net.Conn) (net.Conn, bool) {
	if err := stream.SetReadDeadline(time.Now().Add(t.headerTimeout)); err != nil {
		// Not served, and not unavailable: this is a fact about the stream, which
		// took no deadline. No local service was named yet, so there is nothing
		// to call unreachable.
		t.refuse(stream, fmt.Errorf("%w: arming the request deadline: %v", tunnel.ErrStreamNotServed, err))
		return nil, false
	}

	tag, target, err := tunnel.ReadStreamRequest(stream)
	if err != nil {
		// Two causes, and they stay apart. A frame that is malformed is a bug of
		// the frontend and does not clear, so it is evidence that the frontend
		// acts on. A deadline that expired is a frame that has not arrived yet,
		// and that clears when the link has room. A model that is evicted
		// across the fleet because a link was congested is the failure that this
		// distinction prevents.
		if reportsTimeout(err) {
			t.refuse(stream, fmt.Errorf("%w: %v", tunnel.ErrStreamNotServed, err))
			return nil, false
		}
		t.refuse(stream, fmt.Errorf("%w: %v", tunnel.ErrStreamRequestInvalid, err))
		return nil, false
	}

	svc, known := t.services[tag]
	if !known {
		// A fact about what this worker serves. The frontend knows that a retry
		// is useless until the worker is upgraded, which is not what it should
		// conclude from "unavailable".
		t.refuse(stream, fmt.Errorf("%w: %q", tunnel.ErrStreamTagUnknown, tag))
		return nil, false
	}

	// The deadline is cleared before the local dial. What follows the request
	// frame belongs to the carried protocol, which has its own deadlines, and a
	// deadline that stayed would end a long inference stream.
	if err := stream.SetReadDeadline(time.Time{}); err != nil {
		t.refuse(stream, fmt.Errorf("%w: clearing the request deadline: %v", tunnel.ErrStreamNotServed, err))
		return nil, false
	}

	local, err := svc(ctx, target)
	if err != nil {
		t.refuse(stream, classifyServiceFailure(err))
		return nil, false
	}

	if err := tunnel.WriteStreamAccepted(stream); err != nil {
		// The frontend never learns that the stream was accepted, so it cannot be
		// used. The local connection is closed here, or it would leak once for
		// every reply that failed.
		xlog.Debug("worker tunnel could not accept a stream", "node", t.nodeID, "error", err)
		_ = local.Close()
		_ = stream.Close()
		return nil, false
	}
	return local, true
}

// classifyServiceFailure decides which refusal the error of a local service is.
//
// A service that classified its failure keeps its classification, and that is
// asked of the whole vocabulary. loopbackService classifies, and the difference
// matters: a target outside the port range of the worker is a request that this
// worker never serves, and a backend that does not listen yet is a condition
// that clears. The first reported as the second makes the frontend retry what
// cannot work. The second reported as the first makes it give up on a backend
// that is starting.
//
// The default is "this worker learned nothing", and "unavailable" is an allow
// list. The frontend acts on "unavailable": it counts as evidence that can remove
// the rows of a model. A wrong removal is active. It deletes rows and shuts down
// a model that is loaded. A wrong retention is passive, is limited to one slot of
// one replica, and clears at a restart or an eviction. Because of this, only an
// error that says the target did not answer counts. A dial can also fail for a
// reason in this process or on this host: no file descriptors (EMFILE, ENFILE),
// no buffers or memory (ENOBUFS, ENOMEM), no local port (EADDRNOTAVAIL), or a
// local rule (EACCES, EPERM). The target was never asked in those cases, and a
// reading of them as "the backend is gone" would remove a healthy model when
// this worker is short of descriptors. An error that nobody classified is the
// same: it can be silent for ever, and that is the cheaper mistake.
//
// The errors that mean the target did not answer are in targetUnreachable. The
// end of the context, the own deadline of this process and anything that says of
// itself that it is a timeout are never among them. The context ends while
// stream goroutines run, because this worker is closing its tunnel. A timeout of
// connect(2) means that the handshake did not finish, which is a listener that is
// blocked or full and not one that is absent.
//
// It must never give the unknown-tag refusal: a tag that the worker serves does
// not stop being served because one dial failed.
func classifyServiceFailure(err error) error {
	if tunnel.IsStreamRefusal(err) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || reportsTimeout(err) {
		return fmt.Errorf("%w: %v", tunnel.ErrStreamNotServed, err)
	}
	if targetUnreachable(err) {
		return fmt.Errorf("%w: %v", tunnel.ErrStreamTargetUnavailable, err)
	}
	return fmt.Errorf("%w: %v", tunnel.ErrStreamNotServed, err)
}

// targetUnreachable reports whether err says that the dial reached the network
// stack and the target did not answer: nothing listens, or the route is gone, or
// the peer reset the connection.
func targetUnreachable(err error) bool {
	for _, errno := range unreachableErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// reportsTimeout reports whether err says of itself that it is a timeout.
//
// It is asked in two places that mean different things. On a read of a stream it
// is a deadline that this process set. On a dial it is wider: Go reports ETIMEDOUT
// and EAGAIN as timeouts, and net.OpError passes that on. Both places want the
// same answer, "not the target speaking", so one predicate serves both. See
// classifyServiceFailure.
//
// It asks net.Error as well as os.ErrDeadlineExceeded because the two sets
// differ: a yamux stream returns its own timeout value from a read whose
// deadline passed, and a net.OpError on a socket returns os.ErrDeadlineExceeded.
func reportsTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// refuse says why a stream is not served and then ends it.
//
// The close is not optional. A worker that says why and leaves the stream open
// has left the frontend with a request that is never answered, which looks like
// a worker that is slow, and a deadline on the other side cannot tell the two
// apart. The reply makes the refusal readable and the close makes it prompt. The
// reply is best effort and the close is not.
func (t *Tunnel) refuse(stream net.Conn, reason error) {
	if err := tunnel.WriteStreamRefusal(stream, reason); err != nil {
		xlog.Debug("worker tunnel could not report why it refused a stream", "node", t.nodeID, "error", err)
	}
	_ = stream.Close()
	xlog.Debug("worker tunnel refused a stream", "node", t.nodeID, "reason", reason)
}

// dial opens the websocket for a lane and starts the session on it.
func (t *Tunnel) dial(ctx context.Context, lane tunnel.Lane) (*tunnel.Session, error) {
	// Read here, once for every dial. See TunnelConfig.Token.
	token := t.token()
	if token == "" {
		// Not a dial that fails with "unauthorized". The worker has no
		// credential yet, and an operator who reads "unauthorized" looks for a
		// mismatch that does not exist.
		return nil, errors.New("dialling the worker tunnel: this node has no tunnel credential yet")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	endpoint := t.endpoint
	if lane == tunnel.LaneBulk {
		endpoint += "&lane=" + url.QueryEscape(string(lane))
	}
	ws, resp, err := t.dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if resp != nil {
			// gorilla gives ErrBadHandshake for every status that is not 101.
			// Without the status, a 401, a 403 and a 503 are one log line.
			defer func() { _ = resp.Body.Close() }()
			return nil, &tunnelDialError{status: resp.StatusCode, cause: err}
		}
		return nil, fmt.Errorf("dialling the worker tunnel: %w", err)
	}
	sess, err := tunnel.ClientSession(ws, lane)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	return sess, nil
}

// tunnelDialError carries the status of a dial that the frontend refused, so
// that the refusals are not one line in the log.
type tunnelDialError struct {
	status int
	cause  error
}

func (e *tunnelDialError) Error() string {
	return fmt.Sprintf("dialling the worker tunnel: frontend answered %d: %v", e.status, e.cause)
}

func (e *tunnelDialError) Unwrap() error { return e.cause }

// isWrongReplica reports whether the frontend answered 409 to a dial.
func isWrongReplica(err error) bool {
	var dialErr *tunnelDialError
	return errors.As(err, &dialErr) && dialErr.status == http.StatusConflict
}

// wrongReplicaDelay returns a short wait with jitter.
func wrongReplicaDelay() time.Duration {
	// #nosec G404 -- the jitter spreads dials. An attacker gains nothing by
	// predicting it.
	return tunnelWrongReplicaDelay/2 + time.Duration(rand.Int64N(int64(tunnelWrongReplicaDelay)))
}

// logSessionEnded says why the tunnel connects again, at a level that fits what
// the operator can do about it.
//
// "Awaiting approval", "your token is wrong" and "this frontend has no tunnels"
// send an operator to three places, and a worker retries all three in the same
// way. None of them is a reason to stop, because a registration or an action of
// an admin fixes each without a restart of the worker.
func (t *Tunnel) logSessionEnded(err error, attempt int, delay time.Duration) {
	if err == nil {
		xlog.Info("Worker tunnel closed, connecting again", "node", t.nodeID, "attempt", attempt, "retry_in", delay)
		return
	}

	var dialErr *tunnelDialError
	if errors.As(err, &dialErr) {
		switch dialErr.status {
		case http.StatusUnauthorized:
			// Two causes, and this worker cannot fix either alone. It has no
			// inbound listener, so a tunnel that it cannot open is a worker that
			// nothing can reach. That is an outage and not a warning about a
			// path that is worse.
			xlog.Warn("Frontend rejected this worker's tunnel credential, so nothing can reach this worker; "+
				"either another worker registered under this node name and rotated the credential (check LOCALAI_NODE_NAME is unique), "+
				"or the frontend's record of this node was replaced. Restarting this worker registers it again and mints a new credential",
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
	xlog.Warn("Worker tunnel ended, connecting again", "node", t.nodeID, "attempt", attempt, "retry_in", delay, "error", err)
}

// tunnelBackoffDelay returns the wait before attempt n.
//
// Half of the wait is fixed and half is drawn. Full jitter draws over the whole
// interval and can give a wait near zero, and a worker that can draw that can
// spin. The fixed half is a floor for every worker, and the drawn half keeps a
// fleet that lost the same replica from connecting again at the same moment.
func tunnelBackoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := tunnelBackoffMax
	// Two guards. The bound on attempt keeps the shift defined. The check for a
	// positive value catches the overflow that would give a long outage a
	// negative wait, which is a tight loop that looks like a backoff.
	if attempt <= 40 {
		if scaled := tunnelBackoffBase << (attempt - 1); scaled > 0 && scaled < tunnelBackoffMax {
			d = scaled
		}
	}
	// #nosec G404 -- the jitter spreads attempts so that a fleet does not retry
	// together. An attacker gains nothing by predicting it.
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// tunnelSleep waits for d, or returns early when ctx ends.
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

// tunnelEndpoint turns the URL that a worker registers with into the websocket
// URL of its tunnel. The URL ends with a query, so a lane is added with "&".
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
	// Appended and not assigned, so that a frontend behind a path prefix keeps
	// it. Registration builds its URLs in the same way.
	u.Path = strings.TrimRight(u.Path, "/") + tunnel.ConnectPath
	u.RawQuery = url.Values{"id": []string{nodeID}}.Encode()
	return u.String(), nil
}

// loopbackService routes a tagged stream to a process that listens on the
// loopback interface of this worker.
//
// The host that the frontend names is discarded and only the port is used. This
// is the security property of the function. A tunnel ends inside the worker
// process, so a stream that arrives on it can reach whatever the worker can
// reach. Without this, whoever holds the frontend end could make every worker of
// the fleet dial any host of its private network, and the tunnel would be a
// proxy into the LAN of the worker. Without the host, the set that can be
// reached is this machine.
//
// The port range is the one that the port allocator of the worker gives to its
// backend processes, so a stream cannot be pointed at another service that
// happens to listen on this host. It is as narrow as that range. By default the
// range goes to 65535, and a deployment that wants it narrow sets
// LOCALAI_GRPC_MAX_PORT, which narrows both.
//
// The shape matters as much as the checks. Nothing that comes from the wire
// reaches the dialler. The address is built from the constant loopbackHost and
// from strconv.Itoa of an int that this function checked, so target has no path
// to DialContext. To turn this into a dialler for any host, a data flow has to
// be added, and a deleted check is not enough. That is the difference between a
// property and a guard.
func loopbackService(minPort, maxPort int) LocalService {
	return func(ctx context.Context, target string) (net.Conn, error) {
		_, portStr, err := net.SplitHostPort(target)
		if err != nil {
			return nil, fmt.Errorf("%w: routing a tunnel stream: %q is not a host:port: %v",
				tunnel.ErrStreamRequestInvalid, target, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("%w: routing a tunnel stream: %q has no numeric port: %v",
				tunnel.ErrStreamRequestInvalid, target, err)
		}
		if port < minPort || port > maxPort {
			// Invalid and not unavailable: no retry brings a port outside the
			// range of the allocator into it.
			return nil, fmt.Errorf("%w: routing a tunnel stream: port %d is outside this worker's backend range [%d, %d]",
				tunnel.ErrStreamRequestInvalid, port, minPort, maxPort)
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(port)))
	}
}

// loopbackHost is the host on which every stream that the frontend can steer is
// dialled. It is a constant so that "a stream cannot choose where the worker
// dials" is a fact about the code and not a claim about its inputs.
//
// It is not the only host that this file dials. fixedService dials the address
// it was made with, and Run makes it from LOCALAI_HTTP_ADDR of this worker,
// which an operator may set to an address that can be routed. loopbackAddr
// replaces a wildcard bind and leaves an explicit host alone, because a server
// bound to one address is not reachable on another. So the http tag can dial a
// host that is not loopback. That host was set by the operator for the own
// server of this worker, and no stream names it, because fixedService ignores
// its target. What the design needs is that the frontend cannot steer the dial,
// and that holds for both tags.
const loopbackHost = "127.0.0.1"

// tunnelServices builds the routing table that the worker installs on its
// tunnel. It is its own function so that the table can be tested: the table is
// the security boundary of the tunnel, and a table built inline in Run could be
// reached only by starting a worker.
func tunnelServices(cfg *Config, httpBindAddr string) map[string]LocalService {
	basePort := cfg.effectiveBasePort()
	return map[string]LocalService{
		// The frontend names a backend process by its port. The worker decides
		// that only its own loopback is reachable, and only inside its own port
		// range.
		tunnel.StreamTagGRPC: loopbackService(basePort, cfg.effectiveMaxPort(basePort)),
		tunnel.StreamTagHTTP: fixedService(loopbackAddr(httpBindAddr)),
	}
}

// fixedService routes a tagged stream to one address of this worker and ignores
// what the frontend named. A worker has one HTTP server and only the worker
// knows where it listens, so the frontend has nothing useful to say about the
// target and is not given the chance.
func fixedService(addr string) LocalService {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

// loopbackAddr turns a bind address into one that reaches the same listener from
// inside this process. A server bound to 0.0.0.0 can be reached on loopback, but
// to dial 0.0.0.0 reaches it only by accident and not on every platform, so the
// wildcard is replaced and not dialled.
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
