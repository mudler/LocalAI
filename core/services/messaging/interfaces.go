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

// Broadcaster is the fan-out half of the messaging surface: a publish reaches
// every subscriber on every replica. It exists so a call site can be moved onto
// the PostgreSQL carrier without waiting for the request/reply and queue-group
// halves to be retired, because both *messaging.Client and *pgbus.Bus satisfy
// it.
//
// IsConnected and Close are deliberately NOT here, and their absence is stated
// rather than left to be inferred, because a reader who knows MessagingClient
// assumes the smaller interface simply forgot them.
//
// IsConnected has no production consumer: its only non-test occurrences are its
// implementations and the MessagingClient line itself. It is asserted by specs
// and read by logging, and a carrier's consumers must not branch on it. "The
// carrier is down" is not one of the four conditions a node's state can be in,
// and no code may turn it into evidence that a worker is absent.
//
// Close DOES have real callers, and they hold a CONCRETE type rather than this
// interface, which is why the interface can omit it. Before any of them is
// narrowed from MessagingClient to Broadcaster, re-run
//
//	grep -rn 'Close()' --include='*.go' . | grep -v _test
//
// because that grep is the only thing standing between "the interface does not
// need it" and a lifecycle that silently stops running.
type Broadcaster interface {
	Publisher
	Subscribe(subject string, handler func([]byte)) (Subscription, error)
}

// The concrete NATS client is one of the two carriers this interface exists to
// make interchangeable. Asserted here rather than left to the first adopter, so
// a change to Client's signatures fails to compile in the package that owns the
// interface instead of in whichever call site is migrated next.
var _ Broadcaster = (*Client)(nil)
