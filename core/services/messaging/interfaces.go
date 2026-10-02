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

// Broadcaster is the fan-out surface: a publish reaches every subscriber on
// every replica. Consumers that only publish and subscribe depend on this
// instead of the wide client, so a second carrier only has to honour two
// methods. Delivery is at-most-once, and a consumer must not read silence as
// evidence about a node.
type Broadcaster interface {
	Publisher
	Subscribe(subject string, handler func([]byte)) (Subscription, error)
}

// MessagingClient is the full NATS surface: fan-out plus queue groups and
// request/reply. Only the code that owns a queue or a control request needs it.
type MessagingClient interface {
	Broadcaster
	QueueSubscribe(subject, queue string, handler func([]byte)) (Subscription, error)
	QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
	IsConnected() bool
	Close()
}
