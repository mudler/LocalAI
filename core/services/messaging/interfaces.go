package messaging

import "time"

// Publisher publishes JSON-encoded messages to NATS subjects.
type Publisher interface {
	Publish(subject string, data any) error
}

// Subscription represents a NATS subscription that can be unsubscribed.
type Subscription interface {
	Unsubscribe() error
}

// MessagingClient is the full interface for NATS messaging operations.
// Consumers should depend on this interface rather than the concrete Client
// for testability.
type MessagingClient interface {
	Publisher
	Subscribe(subject string, handler func([]byte)) (Subscription, error)
	QueueSubscribe(subject, queue string, handler func([]byte)) (Subscription, error)
	QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
	IsConnected() bool
	Close()
}
