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

// RequestReply is request and reply between processes: the chat start, the
// replay of chat events and the background jobs of a turn. A request that
// nobody serves fails with an error that IsNoResponders reports. A request
// that the carrier could not deliver fails with ErrNotDelivered, which says
// nothing about who serves the subject.
type RequestReply interface {
	QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	Requester
}

// Requester sends a request and waits for its reply.
type Requester interface {
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// InstanceRequester sends a request to one named process. A carrier on which
// the asking process routes the request implements it. On a carrier that
// routes by subject, such as NATS, the subject names the process already.
type InstanceRequester interface {
	RequestInstance(instance, subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// QueueRequester sends a request to one member of the queue group that serves
// subject. A carrier on which the asking process picks the member implements
// it. moveOn reads a reply and returns true when the member refused in a way
// that proves it did nothing; the carrier then tries another member, and
// returns the last reply when none is left.
type QueueRequester interface {
	RequestQueue(subject string, data []byte, timeout time.Duration, moveOn func(reply []byte) bool) ([]byte, error)
}

// RequestInstance sends data to the process instance through r when r can
// address a process, and on subject alone otherwise.
func RequestInstance(r Requester, instance, subject string, data []byte, timeout time.Duration) ([]byte, error) {
	if a, ok := r.(InstanceRequester); ok {
		return a.RequestInstance(instance, subject, data, timeout)
	}
	return r.Request(subject, data, timeout)
}

// RequestQueue sends data to one member of the queue group of subject through
// r when r picks the member, and on subject alone otherwise, where the carrier
// picks it and moveOn is not used.
func RequestQueue(r Requester, subject string, data []byte, timeout time.Duration, moveOn func([]byte) bool) ([]byte, error) {
	if q, ok := r.(QueueRequester); ok {
		return q.RequestQueue(subject, data, timeout, moveOn)
	}
	return r.Request(subject, data, timeout)
}

// MessagingClient is the full NATS surface: fan-out plus queue groups and
// request/reply. Only the code that owns a queue or a control request needs it.
type MessagingClient interface {
	Broadcaster
	RequestReply
	QueueSubscribe(subject, queue string, handler func([]byte)) (Subscription, error)
	IsConnected() bool
	Close()
}
