package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// DefaultMaxConcurrent is how many claims of one kind a replica drives at once.
//
// A claim is driven while the response of the worker is read: the progress, the
// events and the terminal line all arrive on it, and an MCP CI job holds it for
// up to ten minutes. With one slot, the second queued job of a deployment would
// wait for the first. The limit is for each kind, so a long job of one kind does
// not keep another kind waiting.
const DefaultMaxConcurrent = 4

// DispatchConfig is everything a DispatchLoop needs.
type DispatchConfig struct {
	DB *gorm.DB
	// Owner is the id of this replica in the instances table.
	Owner     string
	Picker    AgentPicker
	Control   StreamCaller
	Broadcast ProgressBroadcaster
	Store     TerminalWriter
	// Hints carries the wake hints of the queue, the cancel of jobs and, for the
	// handlers, the place for events.
	Hints messaging.Broadcaster
	// Interval is the poll, DefaultClaimInterval when zero.
	Interval time.Duration
	// Liveness is the window of replica liveness, cluster.InstanceLiveness when
	// zero.
	Liveness time.Duration
	// MaxConcurrent is how many claims of a kind this replica drives at once,
	// DefaultMaxConcurrent when zero.
	MaxConcurrent int
}

// DispatchLoop is what a frontend replica runs on the tunnel carrier: it claims
// the queued work of every kind and drives it on agent workers. It is a
// ClaimConsumer whose handlers are the AgentDriver.
type DispatchLoop struct {
	driver   *AgentDriver
	consumer *ClaimConsumer
	max      int
	subs     []messaging.Subscription
}

// NewDispatchLoop returns the loop of one replica. Every refusal is a piece of
// wiring whose absence has no symptom: a loop with no picker claims rows and
// releases them for ever, and one with no owner writes claims that the reap
// cannot attribute. Both look like work that is accepted and never done, which
// cannot be told at the API from a busy deployment.
func NewDispatchLoop(cfg DispatchConfig) (*DispatchLoop, error) {
	if cfg.Hints == nil {
		return nil, errors.New("the dispatch loop was built with no bus for wake hints and cancels")
	}
	driver, err := NewAgentDriver(AgentDriverConfig{
		Picker: cfg.Picker, Control: cfg.Control, Broadcast: cfg.Broadcast, Store: cfg.Store, Hints: cfg.Hints,
	})
	if err != nil {
		return nil, err
	}
	consumer, err := NewClaimConsumer(ClaimConsumerConfig{
		DB: cfg.DB, Owner: cfg.Owner, Events: cfg.Hints, Hints: cfg.Hints,
		Interval: cfg.Interval, Liveness: cfg.Liveness,
	})
	if err != nil {
		return nil, err
	}
	max := cfg.MaxConcurrent
	if max <= 0 {
		max = DefaultMaxConcurrent
	}
	return &DispatchLoop{driver: driver, consumer: consumer, max: max}, nil
}

// Start begins to claim and drive until ctx ends or Stop is called.
func (l *DispatchLoop) Start(ctx context.Context) error {
	if err := l.driver.Start(ctx); err != nil {
		return err
	}
	for _, kind := range []messaging.WorkKind{messaging.WorkMCPCI, messaging.WorkAgentRun, messaging.WorkTask} {
		h, err := l.driver.Handler(kind)
		if err != nil {
			l.Stop()
			return err
		}
		sub, err := l.consumer.Consume(ctx, kind, l.max, h)
		if err != nil {
			l.Stop()
			return fmt.Errorf("consuming the %s claims: %w", kind, err)
		}
		l.subs = append(l.subs, sub)
	}
	return nil
}

// Stop stops claiming and waits until every run in flight has been settled. A
// claim that a process leaves held can be released by no other replica until the
// liveness window has passed, so Stop does not return before the claims are
// settled.
func (l *DispatchLoop) Stop() {
	if l == nil {
		return
	}
	for _, sub := range l.subs {
		_ = sub.Unsubscribe()
	}
	l.subs = nil
	l.driver.Stop()
}
