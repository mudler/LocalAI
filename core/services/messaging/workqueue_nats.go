package messaging

import (
	"context"
	"fmt"
)

// natsRoute is the one place that maps a kind onto a NATS subject and queue
// group. jobs.new and jobs.mcp-ci.new share the "workers" group on purpose:
// changing a group changes which processes compete for a message.
func natsRoute(kind WorkKind) (subject, queue string, err error) {
	switch kind {
	case WorkTask:
		return SubjectJobsNew, QueueWorkers, nil
	case WorkMCPCI:
		return SubjectMCPCIJobsNew, QueueWorkers, nil
	case WorkAgentRun:
		return SubjectAgentExecute, QueueAgentWorkers, nil
	default:
		return "", "", fmt.Errorf("unknown work kind %q", kind)
	}
}

type natsWorkQueue struct {
	pub Publisher
}

// NewNATSWorkQueue returns a WorkQueue that publishes on the kind's subject.
// The queue group is a consumer concern, so the producer only needs Publish.
func NewNATSWorkQueue(pub Publisher) WorkQueue {
	return &natsWorkQueue{pub: pub}
}

func (q *natsWorkQueue) Enqueue(_ context.Context, kind WorkKind, payload any) error {
	subject, _, err := natsRoute(kind)
	if err != nil {
		return err
	}
	// Publish marshals payload itself; handing it pre-encoded bytes would
	// double encode.
	return q.pub.Publish(subject, payload)
}
