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
//
// A row that the new carrier refuses goes back to pending, so that nothing is
// lost while the new carrier is down: the rows wait in the table, and the next
// sweep (the handoff runs again after a pause) or the next period on the tunnel
// takes them. The rows that were moved stay as "migrated" for a day and are then
// purged with their payloads.
func (c *ClaimWork) handoff() func(context.Context, *Set) error {
	return func(ctx context.Context, next *Set) error {
		rows, err := jobs.MigratePending(ctx, c.DB)
		if err != nil {
			return err
		}
		var failed error
		var refused []string
		moved := 0
		for _, r := range rows {
			if err := next.WorkQueue.Enqueue(ctx, messaging.WorkKind(r.Kind), json.RawMessage(r.Payload)); err != nil {
				failed = errors.Join(failed, fmt.Errorf("publishing the %s unit %s: %w", r.Kind, r.ID, err))
				refused = append(refused, r.ID)
				continue
			}
			moved++
		}
		if len(refused) > 0 {
			// Not bound to ctx: the reason for the failure may be that it ended.
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := jobs.RestorePending(restoreCtx, c.DB, refused); err != nil {
				failed = errors.Join(failed, fmt.Errorf("putting %d refused units back in the queue: %w", len(refused), err))
			}
		}
		if len(rows) > 0 {
			xlog.Info("Queued work moved to the carrier that took over", "carrier", next.Name, "moved", moved, "kept_in_queue", len(refused))
		}
		if _, err := jobs.PurgeMigrated(ctx, c.DB, jobs.MigratedRetention); err != nil {
			failed = errors.Join(failed, err)
		}
		return failed
	}
}
