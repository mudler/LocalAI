package messaging

import (
	"context"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/pkg/concurrency"
	"github.com/mudler/xlog"
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

type natsWorkRoutes struct {
	agentSubject, agentQueue string
}

// WorkRouteOption changes where a NATS WorkConsumer listens.
type WorkRouteOption func(*natsWorkRoutes)

// WithAgentRunRoute moves the agent-run subject and queue group, which workers
// let operators set (LOCALAI_AGENT_SUBJECT, LOCALAI_AGENT_QUEUE). An empty
// value keeps the default. The other kinds have no such setting.
func WithAgentRunRoute(subject, queue string) WorkRouteOption {
	return func(r *natsWorkRoutes) {
		r.agentSubject, r.agentQueue = subject, queue
	}
}

type natsWorkConsumer struct {
	c      MessagingClient
	routes natsWorkRoutes
}

// NewNATSWorkConsumer returns a WorkConsumer that joins the kind's queue group.
// The handler's events publisher is c itself.
func NewNATSWorkConsumer(c MessagingClient, opts ...WorkRouteOption) WorkConsumer {
	w := &natsWorkConsumer{c: c}
	for _, o := range opts {
		o(&w.routes)
	}
	return w
}

func (w *natsWorkConsumer) route(kind WorkKind) (subject, queue string, err error) {
	subject, queue, err = natsRoute(kind)
	if err != nil || kind != WorkAgentRun {
		return subject, queue, err
	}
	if w.routes.agentSubject != "" {
		subject = w.routes.agentSubject
	}
	if w.routes.agentQueue != "" {
		queue = w.routes.agentQueue
	}
	return subject, queue, nil
}

// natsWorkSubscription tracks in-flight handlers so Unsubscribe can wait for
// them. closed stops a delivery that got past NATS before the unsubscribe from
// calling wg.Add while Unsubscribe is already in wg.Wait.
type natsWorkSubscription struct {
	sub    Subscription
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func (s *natsWorkSubscription) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

// Unsubscribe stops delivery first so no new handler starts, then waits for
// the running ones, as the agent dispatcher's Stop always has.
func (s *natsWorkSubscription) Unsubscribe() error {
	err := s.sub.Unsubscribe()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wg.Wait()
	return err
}

// Consume keeps the two concurrency models the workers had before this seam.
// With maxInFlight 1 the handler runs on the NATS delivery goroutine: one unit
// at a time per worker, messages already handed to this subscription wait
// behind it, and a panic is not recovered. Any other value spawns a recovered
// goroutine per delivery; when bounded, the slot is taken on the delivery
// goroutine, so a full worker stops draining its subscription instead of
// piling up goroutines, and ctx cancellation releases a delivery that waits.
func (w *natsWorkConsumer) Consume(ctx context.Context, kind WorkKind, maxInFlight int, h WorkHandler) (Subscription, error) {
	subject, queue, err := w.route(kind)
	if err != nil {
		return nil, err
	}
	ws := &natsWorkSubscription{}
	run := func(payload []byte) {
		if err := h(ctx, payload, w.c); err != nil {
			xlog.Warn("Work handler could not serve a delivery", "kind", kind, "subject", subject, "error", err)
		}
	}

	var deliver func([]byte)
	switch {
	case maxInFlight == 1:
		deliver = func(payload []byte) {
			if !ws.begin() {
				return
			}
			defer ws.wg.Done()
			run(payload)
		}
	default:
		var sem chan struct{}
		if maxInFlight > 0 {
			sem = make(chan struct{}, maxInFlight)
		}
		deliver = func(payload []byte) {
			if sem != nil {
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}
			if !ws.begin() {
				if sem != nil {
					<-sem
				}
				return
			}
			concurrency.SafeGo(func() {
				defer ws.wg.Done()
				if sem != nil {
					defer func() { <-sem }()
				}
				run(payload)
			})
		}
	}

	sub, err := w.c.QueueSubscribe(subject, queue, deliver)
	if err != nil {
		return nil, fmt.Errorf("subscribing to %s: %w", subject, err)
	}
	ws.sub = sub
	return ws, nil
}
