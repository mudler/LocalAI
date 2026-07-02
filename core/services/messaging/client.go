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

// Client wraps a NATS connection and provides helpers for pub/sub and queue subscriptions.
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
// (interface{ OnReconnect(func()) }) rather than being added to MessagingClient,
// so the messaging abstraction stays minimal and standalone/test clients are not
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

// QueueSubscribe creates a queue subscription. Within the same queue group,
// only one subscriber receives each message (load-balanced).
func (c *Client) QueueSubscribe(subject, queue string, handler func([]byte)) (Subscription, error) {
	return c.confirmSubscription(subject, func(conn *nats.Conn) (*nats.Subscription, error) {
		return conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
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

// Request sends a request and waits for a reply (request-reply pattern).
// Returns the raw reply data.
func (c *Client) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	msg, err := c.conn.Request(subject, data, timeout)
	if err != nil {
		return nil, fmt.Errorf("request to %s: %w", subject, err)
	}
	return msg.Data, nil
}

// SubscribeReply creates a subscription that supports replying to requests.
// The handler receives the raw request data and the reply subject.
func (c *Client) SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (Subscription, error) {
	return c.confirmSubscription(subject, func(conn *nats.Conn) (*nats.Subscription, error) {
		return conn.Subscribe(subject, func(msg *nats.Msg) {
			handler(msg.Data, func(replyData []byte) {
				if msg.Reply != "" {
					if err := msg.Respond(replyData); err != nil {
						xlog.Warn("Failed to send NATS reply", "subject", subject, "error", err)
					}
				}
			})
		})
	})
}

// QueueSubscribeReply creates a queue subscription that supports replying to requests.
// Load-balanced across subscribers in the same queue group, with request-reply support.
func (c *Client) QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (Subscription, error) {
	return c.confirmSubscription(subject, func(conn *nats.Conn) (*nats.Subscription, error) {
		return conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
			handler(msg.Data, func(replyData []byte) {
				if msg.Reply != "" {
					if err := msg.Respond(replyData); err != nil {
						xlog.Warn("Failed to send NATS reply", "subject", subject, "error", err)
					}
				}
			})
		})
	})
}

// SubscribeJSON creates a subscription that automatically unmarshals JSON messages.
// Invalid JSON messages are logged and skipped.
func SubscribeJSON[T any](c MessagingClient, subject string, handler func(T)) (Subscription, error) {
	return c.Subscribe(subject, func(data []byte) {
		var evt T
		if err := json.Unmarshal(data, &evt); err != nil {
			xlog.Warn("Failed to unmarshal NATS message", "subject", subject, "error", err)
			return
		}
		handler(evt)
	})
}

// QueueSubscribeJSON creates a queue subscription that automatically unmarshals JSON messages.
// Invalid JSON messages are logged and skipped.
func QueueSubscribeJSON[T any](c MessagingClient, subject, queue string, handler func(T)) (Subscription, error) {
	return c.QueueSubscribe(subject, queue, func(data []byte) {
		var evt T
		if err := json.Unmarshal(data, &evt); err != nil {
			xlog.Warn("Failed to unmarshal NATS message", "subject", subject, "error", err)
			return
		}
		handler(evt)
	})
}

// RequestJSON sends a JSON request-reply via NATS, marshaling the request and
// unmarshaling the reply. This eliminates the repeated marshal/request/unmarshal
// boilerplate across all NATS request-reply call sites.
func RequestJSON[Req, Reply any](c MessagingClient, subject string, req Req, timeout time.Duration) (*Reply, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	replyData, err := c.Request(subject, data, timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request to %s: %w", subject, err)
	}
	var reply Reply
	if err := json.Unmarshal(replyData, &reply); err != nil {
		return nil, fmt.Errorf("unmarshaling reply from %s: %w", subject, err)
	}
	return &reply, nil
}

// Conn returns the underlying NATS connection for advanced usage.
//
// Deprecated: Prefer using the MessagingClient interface methods (Publish, Subscribe, etc.)
// instead of accessing the raw NATS connection. This method couples callers to the
// concrete Client type and bypasses the abstraction layer.
func (c *Client) Conn() *nats.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
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
