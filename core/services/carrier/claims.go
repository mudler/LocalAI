package carrier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// ClaimWork is what the tunnel carrier needs of a frontend replica besides its
// connections: the replica claims the queued work and drives it on agent
// workers, because an agent worker has no database and cannot claim.
type ClaimWork struct {
	DB *gorm.DB
	// Owner is the id of this replica in the instances table.
	Owner string
	// Store records the terminal state of the jobs that the work names.
	Store jobs.TerminalWriter
	// Bus returns the broadcaster holder. The hints of the queue and the events of
	// the runs go through it, so that they follow the sets. It is a function
	// because the holder is built after the first set, and it is read when the work
	// starts.
	Bus func() messaging.Broadcaster
	// Interval is the poll of the claim loop. The default when zero.
	Interval time.Duration
}

// start returns the Start of a tunnel set. The loop dials through the dialer of
// that set and not through the holder: a run that started on the tunnel finishes
// there, whichever carrier is active by then.
func (c *ClaimWork) start(set func() *Set, selector *nodes.AgentSelector, token string) func(context.Context) (func(), error) {
	return func(ctx context.Context) (func(), error) {
		bus := c.Bus()
		loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
			DB:        c.DB,
			Owner:     c.Owner,
			Picker:    selector,
			Control:   nodes.NewControlClient(set().Dialer, token),
			Broadcast: nodes.NewRebroadcaster(bus),
			Store:     c.Store,
			Hints:     bus,
			Interval:  c.Interval,
		})
		if err != nil {
			return nil, err
		}
		if err := loop.Start(ctx); err != nil {
			return nil, err
		}
		xlog.Info("This replica claims queued work", "owner", c.Owner)
		return loop.Stop, nil
	}
}

// handoff returns the Handoff of a tunnel set: what is still queued moves to the
// queue of the carrier that took over. The rows are taken out of the table first
// and then published, so a job is lost, and not run twice, if this replica dies in
// between. A job that is lost stays running until the reaper fails it.
func (c *ClaimWork) handoff() func(context.Context, *Set) error {
	return func(ctx context.Context, next *Set) error {
		rows, err := jobs.MigratePending(ctx, c.DB)
		if err != nil {
			return err
		}
		var failed error
		moved := 0
		for _, r := range rows {
			if err := next.WorkQueue.Enqueue(ctx, messaging.WorkKind(r.Kind), json.RawMessage(r.Payload)); err != nil {
				failed = errors.Join(failed, fmt.Errorf("publishing the %s unit %s: %w", r.Kind, r.ID, err))
				continue
			}
			moved++
		}
		if len(rows) > 0 {
			xlog.Info("Queued work moved to the carrier that took over", "carrier", next.Name, "moved", moved, "lost", len(rows)-moved)
		}
		return failed
	}
}
