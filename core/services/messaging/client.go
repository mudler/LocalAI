package messaging

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"
)

// Client wraps a NATS connection and provides helpers for pub/sub and queue subscriptions.
type Client struct {
	conn *nats.Conn
	mu   sync.RWMutex
}

// New creates a new NATS client with auto-reconnect.
func New(url string) (*Client, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				xlog.Warn("NATS disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			xlog.Info("NATS reconnected")
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			xlog.Info("NATS connection closed")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS at %s: %w", url, err)
	}

	return &Client{conn: nc}, nil
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
func (c *Client) Subscribe(subject string, handler func([]byte)) (*nats.Subscription, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})
}

// QueueSubscribe creates a queue subscription. Within the same queue group,
// only one subscriber receives each message (load-balanced).
func (c *Client) QueueSubscribe(subject, queue string, handler func([]byte)) (*nats.Subscription, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		handler(msg.Data)
	})
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
func (c *Client) SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (*nats.Subscription, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data, func(replyData []byte) {
			if msg.Reply != "" {
				if err := msg.Respond(replyData); err != nil {
					xlog.Warn("Failed to send NATS reply", "subject", subject, "error", err)
				}
			}
		})
	})
}

// QueueSubscribeReply creates a queue subscription that supports replying to requests.
// Load-balanced across subscribers in the same queue group, with request-reply support.
func (c *Client) QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (*nats.Subscription, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		handler(msg.Data, func(replyData []byte) {
			if msg.Reply != "" {
				if err := msg.Respond(replyData); err != nil {
					xlog.Warn("Failed to send NATS reply", "subject", subject, "error", err)
				}
			}
		})
	})
}

// SubscribeJSON creates a subscription that automatically unmarshals JSON messages.
// Invalid JSON messages are logged and skipped.
func SubscribeJSON[T any](c *Client, subject string, handler func(T)) (*nats.Subscription, error) {
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
func QueueSubscribeJSON[T any](c *Client, subject, queue string, handler func(T)) (*nats.Subscription, error) {
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
func RequestJSON[Req, Reply any](c *Client, subject string, req Req, timeout time.Duration) (*Reply, error) {
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
