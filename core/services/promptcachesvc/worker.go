package promptcachesvc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// Worker is the worker half of the disk tier in distributed mode. It holds the
// settings the frontends last sent and applies their purges to the files of
// this worker, which it alone owns. The settings live in memory: a worker that
// starts runs the defaults until the frontends send the settings again, which
// they do when they see its new incarnation.
type Worker struct {
	// localOff is the switch of this worker (LOCALAI_PROMPT_CACHE_DISK=false).
	// It can only turn the tier off: the cluster settings cannot turn it on
	// on a worker whose operator refused it.
	localOff    bool
	incarnation string
	now         func() time.Time

	// syncMu runs one sync at a time: the HTTP control plane delivers
	// requests concurrently. mu guards the fields below and is never held
	// across a purge, so a backend start that reads the settings never waits
	// for one.
	syncMu   sync.Mutex
	mu       sync.Mutex
	node     *Node
	settings *workerctl.PromptCacheSettings
	// purgedThrough is the highest purge sequence applied since start.
	// It only skips a purge that two frontends send at once; a purge applied
	// again after a restart is bounded by its age (see PromptCachePurge).
	purgedThrough int64
}

// NewWorker returns the worker half. Build the Node with w.Settings and
// WithUsers(w.Users), then Attach it.
func NewWorker(localOff bool, incarnation string) *Worker {
	return &Worker{localOff: localOff, incarnation: incarnation, now: time.Now}
}

// Attach gives the worker the disk tier it acts on.
func (w *Worker) Attach(n *Node) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.node = n
}

// Settings is the configuration in force on this worker: the cluster
// settings, or the defaults before any arrived, with the switch of the worker.
func (w *Worker) Settings() config.PromptCacheConfig {
	w.mu.Lock()
	s := w.settings
	w.mu.Unlock()
	c := config.DefaultPromptCacheConfig()
	if s != nil {
		c = ConfigOf(*s)
	}
	c.DiskDisabled = c.DiskDisabled || w.localOff
	return c
}

// Users reports authentication and the user count the frontends sent, for the
// automatic per-user quota.
func (w *Worker) Users() (bool, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.settings == nil {
		return false, 0
	}
	return w.settings.AuthOn, w.settings.Users
}

// Sync serves promptcache.sync: it takes the settings, then applies the
// purges in order. A purge that fails stops the run, so the frontend sends it
// again from there.
func (w *Worker) Sync(ctx context.Context, req workerctl.PromptCacheSyncRequest) workerctl.PromptCacheSyncReply {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	w.mu.Lock()
	if req.Settings != nil {
		s := *req.Settings
		if w.settings == nil || w.settings.Version != s.Version {
			xlog.Info("prompt cache settings from the cluster", "version", s.Version, "disabled", s.Disabled || w.localOff)
		}
		w.settings = &s
	}
	reply := workerctl.PromptCacheSyncReply{Success: true, Incarnation: w.incarnation}
	if w.settings != nil {
		reply.SettingsVersion = w.settings.Version
	}
	node, through := w.node, w.purgedThrough
	w.mu.Unlock()

	for _, p := range req.Purges {
		if p.Seq <= through {
			continue
		}
		scope := Scope{All: p.All, Model16: p.Model16, NSIDs: p.NSIDs}
		if p.All || p.Model16 != "" {
			scope.Before = w.now().Add(-time.Duration(p.AgeMs) * time.Millisecond)
		}
		if node != nil {
			r, err := node.Purge(ctx, scope)
			reply.Files += r.Files
			reply.Bytes += r.Bytes
			if err != nil && !errors.Is(err, ErrNoScope) {
				reply.Success = false
				reply.Error = err.Error()
				break
			}
		}
		through = p.Seq
	}
	w.mu.Lock()
	w.purgedThrough = through
	w.mu.Unlock()
	reply.PurgedThrough = through
	return reply
}

// Usage serves promptcache.usage.
func (w *Worker) Usage(ctx context.Context) workerctl.PromptCacheUsageReply {
	w.mu.Lock()
	n := w.node
	w.mu.Unlock()
	if n == nil {
		return workerctl.PromptCacheUsageReply{}
	}
	if n.Root() == "" {
		return workerctl.PromptCacheUsageReply{OffReason: OffNoPath}
	}
	enabled, off := n.Enabled(), n.OffReason()
	u, err := n.Usage(ctx)
	if err != nil {
		return workerctl.PromptCacheUsageReply{Enabled: enabled, OffReason: off, Error: err.Error()}
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return workerctl.PromptCacheUsageReply{Enabled: enabled, OffReason: off, Error: err.Error()}
	}
	return workerctl.PromptCacheUsageReply{Enabled: enabled, OffReason: off, Usage: raw}
}
