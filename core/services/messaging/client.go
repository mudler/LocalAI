package messaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// subscribeConfirmTimeout bounds the server round-trip used to detect whether a
// subscription was rejected (e.g. by JWT permissions) before returning to the caller.
const subscribeConfirmTimeout = 5 * time.Second

// Client is a NATS connection, and NO PRODUCTION PATH CONSTRUCTS ONE.
//
// The last family that needed a bus was agent.<name>.cancel, whose subscriber is
// the agent WORKER: it has no database, so it could never join the PostgreSQL
// carrier the rest of the deployment fans out on. It does not need a bus either
// now, because it holds an outward tunnel and a cancel is a control verb on it
// (workerctl.PathAgentCancel). Nothing in core/ or pkg/ calls messaging.New.
//
// What survives here is this type, its connect options and its TLS plumbing,
// still exercised by the NATS JWT permission specs. Deleting them is a
// demolition of its own, together with the JWT minting at registration and
// pkg/natsauth's permission tables.
//
// The methods that carried everything else are already gone: queue
// subscriptions became a claim on the job store, and request/reply became a
// streaming control RPC on the tunnel each worker dials. Deleting the METHODS
// rather than only the call sites is what makes putting a family back on this
// carrier a build error, instead of a line that compiles, publishes
// successfully, and is delivered onto a carrier nothing reads.
type Client struct {
	conn *nats.Conn
	mu   sync.RWMutex

	// reconnectCbs are invoked after the underlying connection is
	// re-established. nats.go transparently resubscribes existing
	// subscriptions on reconnect, but it cannot know that a consumer kept
	// derived in-memory state (e.g. syncstate.SyncedMap) that may have drifted
	// while the link was down — these callbacks let such consumers re-hydrate.
	cbMu         sync.Mutex
	reconnectCbs []func()
}

// New creates a new NATS client with auto-reconnect.
func New(url string, opts ...Option) (*Client, error) {
	var cfg connectConfig
	for _, o := range opts {
		o(&cfg)
	}

	// Allocate the client up front so the reconnect handler closure can reach
	// it; conn is populated after nats.Connect succeeds below.
	c := &Client{}

	natsOpts := []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				xlog.Warn("NATS disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			xlog.Info("NATS reconnected")
			c.runReconnectCallbacks()
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			xlog.Info("NATS connection closed")
		}),
		// Surface async errors (notably permission violations) that NATS would
		// otherwise deliver silently. A subscription the server rejects for a
		// JWT permission means the worker never receives those messages, so make
		// it loud rather than letting the feature fail invisibly.
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			subject := ""
			if sub != nil {
				subject = sub.Subject
			}
			if errors.Is(err, nats.ErrPermissionViolation) {
				xlog.Error("NATS permission violation — check JWT pub/sub allow lists", "subject", subject, "error", err)
				return
			}
			xlog.Warn("NATS async error", "subject", subject, "error", err)
		}),
	}
	switch {
	case cfg.jwtProvider != nil:
		// Fetch creds on every (re)connect so a refresh loop can rotate the JWT
		// before expiry; the server expiring the old JWT triggers a reconnect
		// that transparently picks up the new one.
		natsOpts = append(natsOpts, nats.UserJWT(
			func() (string, error) {
				jwt, _ := cfg.jwtProvider()
				if jwt == "" {
					return "", fmt.Errorf("no NATS user JWT available")
				}
				return jwt, nil
			},
			func(nonce []byte) ([]byte, error) {
				_, seed := cfg.jwtProvider()
				kp, err := nkeys.FromSeed([]byte(seed))
				if err != nil {
					return nil, fmt.Errorf("loading NATS user seed: %w", err)
				}
				defer kp.Wipe()
				return kp.Sign(nonce)
			},
		))
	case cfg.userJWT != "" && cfg.userSeed != "":
		natsOpts = append(natsOpts, nats.UserJWTAndSeed(cfg.userJWT, cfg.userSeed))
	}
	if cfg.tls.Enabled() {
		if err := cfg.tls.Validate(); err != nil {
			return nil, err
		}
		tlsOpts, err := cfg.tls.natsOptions()
		if err != nil {
			return nil, err
		}
		natsOpts = append(natsOpts, tlsOpts...)
	}

	nc, err := nats.Connect(url, natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS at %s: %w", sanitize.URL(url), err)
	}

	c.conn = nc
	return c, nil
}

// OnReconnect registers a callback invoked after the NATS connection is
// re-established. It is consumed via an optional interface type-assertion
// (interface{ OnReconnect(func()) }) rather than being added to Broadcaster, so
// the messaging abstraction stays minimal and standalone/test clients are not
// forced to implement reconnect semantics. A nil callback is ignored.
func (c *Client) OnReconnect(cb func()) {
	if cb == nil {
		return
	}
	c.cbMu.Lock()
	c.reconnectCbs = append(c.reconnectCbs, cb)
	c.cbMu.Unlock()
}

// runReconnectCallbacks invokes registered reconnect callbacks. It copies the
// slice under the lock so a callback that (re)registers cannot deadlock.
func (c *Client) runReconnectCallbacks() {
	c.cbMu.Lock()
	cbs := append([]func(){}, c.reconnectCbs...)
	c.cbMu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

// Publish marshals data as JSON and publishes it to the given subject.
func (c *Client) Publish(subject string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshalling message for %s: %w", subject, err)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn.Publish(subject, payload)
}

// Subscribe creates a subscription on the given subject. All subscribers receive every message.
func (c *Client) Subscribe(subject string, handler func([]byte)) (Subscription, error) {
	return c.confirmSubscription(subject, func(conn *nats.Conn) (*nats.Subscription, error) {
		return conn.Subscribe(subject, func(msg *nats.Msg) {
			handler(msg.Data)
		})
	})
}

// confirmSubscription creates a subscription via mk and forces a server
// round-trip so that a permissions violation — which NATS otherwise reports
// only asynchronously — is returned to the caller synchronously. The server
// emits the "-ERR Permissions Violation" for a rejected SUB before the PONG
// that satisfies the flush, so by the time FlushTimeout returns the violation
// is recorded as the connection's last error. Without this, a worker whose JWT
// lacks a subject gets a non-nil subscription that never receives a message,
// turning a permission misconfiguration into a silent failure.
func (c *Client) confirmSubscription(subject string, mk func(*nats.Conn) (*nats.Subscription, error)) (Subscription, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return nil, fmt.Errorf("subscribe to %s: nil NATS connection", subject)
	}

	sub, err := mk(conn)
	if err != nil {
		return nil, err
	}

	// A failed flush here means we could not round-trip to the server (not yet
	// connected, reconnecting, slow link). RetryOnFailedConnect intentionally
	// buffers subscriptions across that gap, so do NOT fail — keep the
	// subscription and let it replay on (re)connect; a later permission
	// violation is still logged by the async error handler in New.
	if err := conn.FlushTimeout(subscribeConfirmTimeout); err != nil {
		xlog.Debug("Could not confirm NATS subscription (will replay on connect)", "subject", subject, "error", err)
		return sub, nil
	}
	// Flush succeeded, so any permission violation for this SUB has already been
	// recorded as the connection's last error (the server emits it before the
	// PONG). LastError is per-connection; match the exact quoted subject the
	// server echoes ("Subscription to \"<subject>\"") so a stale violation for
	// another subject can't be mis-attributed here.
	if lerr := conn.LastError(); lerr != nil &&
		errors.Is(lerr, nats.ErrPermissionViolation) &&
		strings.Contains(lerr.Error(), `Subscription to "`+subject+`"`) {
		_ = sub.Unsubscribe()
		return nil, fmt.Errorf("subscription to %s denied by NATS server (check JWT sub allow list): %w", subject, lerr)
	}
	return sub, nil
}

// ConfirmRoundTrip forces a round trip to the server and returns whatever the
// server pushed back asynchronously, so that a refusal becomes an error a
// caller holds rather than a line in a log.
//
// It replaces a Conn() accessor that handed out the raw *nats.Conn. That
// accessor was the hole in this type's method set: every half deleted above is
// still one call away on a *nats.Conn, so a family could be put back on this
// carrier through it without a single build error, which is the whole thing the
// deletions are for.
//
// What it does is the publish-side twin of confirmSubscription. NATS reports a
// permission violation asynchronously and does NOT close the connection, so a
// denied publish is indistinguishable from an accepted one until something
// round-trips and reads the connection's last error. A flush that fails is
// returned as-is: the caller could not reach the server at all, which is a
// different fact from the server refusing it, and neither is evidence about any
// node.
//
// No production path calls it, and this carrier no longer has production users
// at all. What needs the verdict is pkg/natsauth's permission grants, which are
// asserted against a real enforcing server and would otherwise be asserted
// against nothing, since an allow list that is EMPTY means unrestricted in NATS
// and a spec that only checks IsConnected cannot tell a granted publish from a
// denied one.
func (c *Client) ConfirmRoundTrip(timeout time.Duration) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("confirming a round trip: nil NATS connection")
	}
	if err := conn.FlushTimeout(timeout); err != nil {
		return fmt.Errorf("round trip to the NATS server: %w", err)
	}
	return conn.LastError()
}

// IsConnected returns true if the client is currently connected to a NATS server.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil && c.conn.IsConnected()
}

// Close drains and closes the NATS connection, waiting for in-flight messages.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Drain()
		c.conn.FlushTimeout(5 * time.Second)
	}
}
