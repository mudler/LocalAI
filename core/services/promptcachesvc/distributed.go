package promptcachesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
	pc "github.com/mudler/LocalAI/pkg/promptcache"
	"github.com/mudler/xlog"
)

// Sender sends the prompt cache verbs to one worker. It is
// nodes.PromptCacheControl; the carrier holder implements it.
type Sender interface {
	PromptCacheSync(ctx context.Context, nodeID string, req workerctl.PromptCacheSyncRequest, timeout time.Duration) (*workerctl.PromptCacheSyncReply, error)
	PromptCacheUsage(ctx context.Context, nodeID string, timeout time.Duration) (*workerctl.PromptCacheUsageReply, error)
}

// Peer is one worker that may hold a disk tier.
type Peer struct {
	ID   string
	Name string
	// Incarnation is the worker process the registry last heard from.
	Incarnation string
	// Online is false for a worker the registry knows to be away: it is not
	// asked, and its purges wait in the log.
	Online bool
}

// Default timings of Distributed.
const (
	DefaultSyncInterval = 30 * time.Second
	DefaultCallTimeout  = 30 * time.Second
	// purgePage bounds the purges of one sync request.
	purgePage = 256
)

// ErrNodeUnreachable marks the report of a worker that did not answer.
var ErrNodeUnreachable = errors.New("the node did not answer")

// Distributed is Nodes for a cluster of workers. Each worker owns the files
// of its tier; the frontends own the settings and the purges, which they keep
// once in the Ledger and send to every worker with promptcache.sync.
//
// Multi-frontend: every frontend may record a purge and fan it out at once;
// the bring-up of workers that missed something (offline, restarted, a
// settings change) runs on one frontend at a time (Lock). A purge sent twice
// is harmless: the worker skips a sequence it applied, and a purge of
// everything or of a model deletes only files used before it was asked for.
type Distributed struct {
	Ledger Ledger
	Sender Sender
	// Peers lists the workers of the cluster.
	Peers func(ctx context.Context) ([]Peer, error)
	// Settings gives the settings in force for the cluster.
	Settings func() workerctl.PromptCacheSettings
	// Lock runs fn on one frontend of the cluster at a time and reports
	// whether it ran. Nil runs fn here.
	Lock func(ctx context.Context, fn func() error) (bool, error)

	Interval  time.Duration
	Timeout   time.Duration
	Retention time.Duration
	Now       func() time.Time

	kick chan struct{}
	once sync.Once
}

var _ Nodes = (*Distributed)(nil)

func (d *Distributed) init() {
	d.once.Do(func() {
		d.kick = make(chan struct{}, 1)
		if d.Interval <= 0 {
			d.Interval = DefaultSyncInterval
		}
		if d.Timeout <= 0 {
			d.Timeout = DefaultCallTimeout
		}
		if d.Retention <= 0 {
			d.Retention = DefaultPurgeRetention
		}
		if d.Now == nil {
			d.Now = time.Now
		}
	})
}

// Purge records the purge in the log, then sends it to every online worker.
// A worker that does not answer gets it from the log when it is back, so the
// purge succeeds once it is recorded. The result sums what the workers that
// answered deleted.
func (d *Distributed) Purge(ctx context.Context, s Scope) (pc.Result, error) {
	d.init()
	if !s.All && s.Model16 == "" && len(s.NSIDs) == 0 {
		return pc.Result{}, ErrNoScope
	}
	if _, err := d.Ledger.AddPurge(ctx, s, d.Now()); err != nil {
		return pc.Result{}, err
	}
	return d.SyncAll(ctx)
}

// Usage asks every worker for its usage. A worker that is offline or does not
// answer is in the report with its error, so the view shows every node.
func (d *Distributed) Usage(ctx context.Context) ([]NodeReport, error) {
	d.init()
	peers, err := d.Peers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeReport, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		out[i] = NodeReport{Node: p.Name}
		if !p.Online {
			out[i].Error = "offline"
			continue
		}
		wg.Add(1)
		go func(i int, p Peer) {
			defer wg.Done()
			out[i] = d.usageOf(ctx, p)
		}(i, p)
	}
	wg.Wait()
	return out, nil
}

func (d *Distributed) usageOf(ctx context.Context, p Peer) NodeReport {
	r := NodeReport{Node: p.Name}
	reply, err := d.Sender.PromptCacheUsage(ctx, p.ID, d.Timeout)
	if err != nil {
		if isNoRoute(err) {
			// No route also covers a worker older than the verb.
			r.Error = ErrNodeUnreachable.Error()
			return r
		}
		r.Error = fmt.Sprintf("%v: %v", ErrNodeUnreachable, err)
		return r
	}
	r.Enabled = reply.Enabled
	r.OffReason = reply.OffReason
	if reply.Error != "" {
		r.Error = reply.Error
	}
	if len(reply.Usage) > 0 {
		if err := json.Unmarshal(reply.Usage, &r.NodeUsage); err != nil {
			r.Error = "unreadable usage: " + err.Error()
		}
	}
	return r
}

// isNoRoute reports an ErrNoRoute of package nodes without importing it: the
// error wraps workerctl.ErrNoRoute.
func isNoRoute(err error) bool { return errors.Is(err, workerctl.ErrNoRoute) }

// SyncAll brings every online worker up to date that needs it, at once, and
// sums what their purges deleted.
func (d *Distributed) SyncAll(ctx context.Context) (pc.Result, error) {
	d.init()
	peers, err := d.Peers(ctx)
	if err != nil {
		return pc.Result{}, err
	}
	state, err := d.Ledger.Nodes(ctx)
	if err != nil {
		return pc.Result{}, err
	}
	last, err := d.Ledger.LastSeq(ctx)
	if err != nil {
		return pc.Result{}, err
	}
	settings := d.Settings()
	var (
		mu    sync.Mutex
		total pc.Result
		wg    sync.WaitGroup
	)
	for _, p := range peers {
		if !p.Online {
			continue
		}
		row, known := state[p.ID]
		if known && !needsSync(p, row, settings.Version, last) {
			continue
		}
		wg.Add(1)
		go func(p Peer, row NodeSync) {
			defer wg.Done()
			r, err := d.syncNode(ctx, p, row, settings)
			if err != nil {
				if isNoRoute(err) {
					xlog.Debug("prompt cache sync: no route to the node; it gets the purges when it is back", "node", p.Name, "error", err)
				} else {
					xlog.Warn("prompt cache sync of a node failed; it gets the purges at the next pass", "node", p.Name, "error", err)
				}
			}
			mu.Lock()
			total.Files += r.Files
			total.Bytes += r.Bytes
			mu.Unlock()
		}(p, row)
	}
	wg.Wait()
	return total, nil
}

// needsSync reports whether a worker is behind: a purge it has not applied,
// a full purge owed, other settings, or a restart that lost them.
func needsSync(p Peer, row NodeSync, version string, last int64) bool {
	return row.PurgedThrough < last ||
		row.FullPurgeAt != nil ||
		row.SettingsVersion != version ||
		(p.Incarnation != "" && p.Incarnation != row.Incarnation)
}

// syncNode sends the settings and the purges the worker has not applied, a
// page at a time, and records each answer.
func (d *Distributed) syncNode(ctx context.Context, p Peer, row NodeSync, settings workerctl.PromptCacheSettings) (pc.Result, error) {
	var total pc.Result
	cursor := row.PurgedThrough
	full := row.FullPurgeAt
	for {
		tombs, err := d.Ledger.PurgesAfter(ctx, cursor, purgePage)
		if err != nil {
			return total, err
		}
		now := d.Now()
		req := workerctl.PromptCacheSyncRequest{Settings: &settings}
		if full != nil {
			// The log dropped purges this worker missed: everything used
			// before the newest of them goes. It carries the sequence of the
			// cursor, which the worker has not seen as a purge.
			req.Purges = append(req.Purges, workerctl.PromptCachePurge{Seq: cursor, All: true, AgeMs: ageMs(now, *full)})
		}
		for _, t := range tombs {
			s := t.Scope()
			req.Purges = append(req.Purges, workerctl.PromptCachePurge{
				Seq: t.Seq, All: s.All, Model16: s.Model16, NSIDs: s.NSIDs, AgeMs: ageMs(now, t.CreatedAt),
			})
		}
		reply, err := d.Sender.PromptCacheSync(ctx, p.ID, req, d.Timeout)
		if err != nil {
			return total, err
		}
		total.Files += reply.Files
		total.Bytes += reply.Bytes
		// The worker says how far it got; on success it got through the
		// page. Its own cursor may be ahead when another frontend synced it.
		next := max(cursor, reply.PurgedThrough)
		if len(tombs) > 0 && reply.Success {
			next = max(next, tombs[len(tombs)-1].Seq)
		}
		var done *time.Time
		if full != nil && reply.Success {
			done = full
		}
		if err := d.Ledger.RecordSync(ctx, NodeSync{
			NodeID: p.ID, Incarnation: reply.Incarnation, SettingsVersion: reply.SettingsVersion, PurgedThrough: next,
		}, done); err != nil {
			return total, err
		}
		if !reply.Success {
			return total, fmt.Errorf("the node refused the sync: %s", reply.Error)
		}
		full = nil
		cursor = next
		if len(tombs) < purgePage {
			return total, nil
		}
	}
}

func ageMs(now, at time.Time) int64 {
	return max(now.Sub(at).Milliseconds(), 0)
}

// Tick prunes the log and brings up the workers that missed something: the
// pass that runs on one frontend at a time.
func (d *Distributed) Tick(ctx context.Context) error {
	d.init()
	peers, err := d.Peers(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(peers))
	for _, p := range peers {
		ids = append(ids, p.ID)
	}
	if err := d.Ledger.EnsureNodes(ctx, ids); err != nil {
		return err
	}
	if err := d.Ledger.ForgetNodes(ctx, ids); err != nil {
		return err
	}
	if n, err := d.Ledger.Prune(ctx, d.Now().Add(-d.Retention)); err != nil {
		return err
	} else if n > 0 {
		xlog.Debug("prompt cache purge log pruned", "purges", n)
	}
	_, err = d.SyncAll(ctx)
	return err
}

// Kick asks the loop for a pass now, for a change of the settings. It never
// blocks.
func (d *Distributed) Kick() {
	d.init()
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run runs Tick every Interval and on each Kick until ctx ends.
func (d *Distributed) Run(ctx context.Context) {
	d.init()
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.kick:
		}
		run := func() error { return d.Tick(ctx) }
		var err error
		if d.Lock != nil {
			_, err = d.Lock(ctx, run)
		} else {
			err = run()
		}
		if err != nil && ctx.Err() == nil {
			xlog.Warn("prompt cache sync pass failed", "error", err)
		}
	}
}
