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

// RequestReply is request and reply on a carrier that routes by subject, such
// as NATS between a frontend and a worker. A request that nobody serves fails
// with an error that IsNoResponders reports.
type RequestReply interface {
	QueueSubscribeReply(subject, queue string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (Subscription, error)
	Requester
}

// Requester sends a request and waits for its reply.
type Requester interface {
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// InstanceRequester sends a request to one named process.
type InstanceRequester interface {
	RequestInstance(instance, subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// QueueRequester sends a request to one member of the queue that serves
// subject; the asking process picks the member. moveOn reads a reply and
// returns true when the member refused in a way that proves it did nothing;
// the carrier then tries another member, and returns the last reply when none
// is left.
type QueueRequester interface {
	RequestQueue(subject string, data []byte, timeout time.Duration, moveOn func(reply []byte) bool) ([]byte, error)
}

// ReplyServer serves the requests of the other processes on subject.
type ReplyServer interface {
	SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (Subscription, error)
}

// ProcessReplies is request and reply between the frontends and the chat
// workers: the chat start, the replay of chat events and the background jobs
// of a turn. The asking process names the process that answers, or the queue
// that one of them serves. It goes over the peer link on every deployment. A
// request that nobody serves fails with an error that IsNoResponders reports.
// A request that could not be delivered fails with ErrNotDelivered, which says
// nothing about who serves the subject.
type ProcessReplies interface {
	ReplyServer
	InstanceRequester
	QueueRequester
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
