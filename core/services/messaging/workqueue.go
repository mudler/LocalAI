package messaging

import "context"

// WorkKind names a unit of competing-consumer work. The values match the claim
// kinds of the self-hosted carrier so the two map one to one.
type WorkKind string

const (
	WorkTask     WorkKind = "task"      // NATS: jobs.new, group "workers"
	WorkMCPCI    WorkKind = "mcp-ci"    // NATS: jobs.mcp-ci.new, group "workers"
	WorkAgentRun WorkKind = "agent-run" // NATS: agent.execute, group "agent-workers"
)

// WorkQueue is the producer side of competing-consumer work: exactly one
// consumer of the kind is meant to run each payload.
//
// A nil error means the carrier accepted the payload, not that any consumer
// exists or will run it. The NATS carrier refuses a payload over the server's
// max_payload (1 MB by default); the Broadcaster's 7999-byte guarantee does not
// apply to the queue.
//
// The NATS carrier ignores ctx: its publish takes none and returns once the
// message is buffered, so there is nothing for a cancellation to interrupt.
type WorkQueue interface {
	Enqueue(ctx context.Context, kind WorkKind, payload any) error
}

// WorkHandler runs one unit of work to its conclusion and returns only then.
// events is where the work publishes its progress, results and agent events. A
// nil return means this worker ran the work (success or failure is reported on
// events); a non-nil return means it could not serve it. Delivery count is
// carrier-defined: a handler must tolerate a repeat.
type WorkHandler func(ctx context.Context, payload []byte, events Publisher) error

// WorkConsumer is the worker side. ctx is the parent of every handler call.
// maxInFlight bounds concurrent handler calls: 0 is unbounded, 1 is serial.
// Unsubscribe stops delivery and waits for in-flight handlers to return.
type WorkConsumer interface {
	Consume(ctx context.Context, kind WorkKind, maxInFlight int, h WorkHandler) (Subscription, error)
}
