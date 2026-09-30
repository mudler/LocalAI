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
