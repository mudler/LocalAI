package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/promptcache"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// startPromptCache starts the disk tier of the prompt cache of this node: the
// root under the data path, the read-speed probe, the janitor loop, the
// environment of the backend processes, and the purge of the namespaces of a
// removed user or client key.
//
// Who owns what (see docs/content/models/disk-prompt-cache.md):
//   - Files: the node that runs the backend, on its own local disk. In
//     distributed mode that is a worker; a frontend never touches them.
//   - Settings: the cluster settings in the database (the memory settings
//     document, shared by every frontend). Workers get them over the control
//     verbs.
//   - Purges: the purge log in the database (promptcachesvc.Ledger). Any
//     frontend records a purge and fans it out; one frontend at a time
//     (advisory lock) brings up the workers that missed something.
//   - Usage: each node reports its own; the frontend asks every node.
//
// In single mode PromptCacheNodes is this node (Local).
func (a *Application) startPromptCache(o *config.ApplicationConfig) {
	node := promptcachesvc.NewNode(o.PromptCacheRoot(), o.SystemState.Model.ModelsPath,
		func() config.PromptCacheConfig { return o.PromptCache },
		promptcachesvc.WithModelTTLs(a.promptCacheModelTTLs),
		promptcachesvc.WithUsers(a.promptCacheUsers),
	)
	// A root on a network disk, or one this node cannot write, turns the
	// tier off here with one WARN.
	node.CheckRoot()
	a.promptCacheMu.Lock()
	a.promptCache = node
	if a.promptCacheNodes == nil {
		a.promptCacheNodes = promptcachesvc.Local{Node: node}
	}
	a.promptCacheMu.Unlock()

	a.ModelLoader().SetBackendEnv(node.BackendEnv)
	o.PromptCacheAutoQuota = func() int64 { return node.Policy().QuotaBytes }
	node.Start(o.Context)
	if a.distributed != nil {
		a.startDistributedPromptCache(o)
	}

	auth.SetPrincipalRemovedHook(func(namespaces []string) {
		go func() {
			ctx, cancel := context.WithTimeout(o.Context, time.Minute)
			defer cancel()
			if _, err := a.PromptCacheNodes().Purge(ctx, promptcachesvc.NamespaceScope(namespaces...)); err != nil {
				xlog.Warn("could not purge the prompt cache of a removed principal", "error", err)
			}
		}()
	})
}

// startDistributedPromptCache makes PromptCacheNodes reach every worker: the
// purges go to the purge log and out to the workers, the usage comes from
// each worker, and a loop brings up the workers that missed a purge or a
// settings change. A failure leaves this frontend on Local, which only acts on
// its own (empty) root, and says so.
func (a *Application) startDistributedPromptCache(o *config.ApplicationConfig) {
	db := a.distributedDB()
	sender, ok := a.distributed.Unloader.(promptcachesvc.Sender)
	if db == nil || !ok {
		xlog.Error("the disk prompt cache of the workers cannot be managed: no database or no control route")
		return
	}
	ledger, err := promptcachesvc.NewDBLedger(db)
	if err != nil {
		xlog.Error("the disk prompt cache of the workers cannot be managed", "error", err)
		return
	}
	registry := a.distributed.Registry
	dist := &promptcachesvc.Distributed{
		Ledger: ledger,
		Sender: sender,
		Peers: func(ctx context.Context) ([]promptcachesvc.Peer, error) {
			return promptCachePeers(ctx, registry)
		},
		Settings: func() workerctl.PromptCacheSettings {
			authOn, users := a.promptCacheUsers()
			return promptcachesvc.WireSettings(a.ApplicationConfig().PromptCache, authOn, users)
		},
		Lock: func(ctx context.Context, fn func() error) (bool, error) {
			return advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyPromptCacheSync, fn)
		},
	}
	// The workers compute the automatic quota from their own cap; a quota
	// from the disk of this frontend would mean nothing to them.
	o.PromptCacheAutoQuota = nil
	a.promptCacheMu.Lock()
	a.promptCacheNodes = dist
	a.promptCacheDist = dist
	a.promptCacheMu.Unlock()
	go dist.Run(o.Context)
	dist.Kick()
}

// promptCachePeers lists the backend workers of the cluster. A pending worker
// is not one yet; an offline one is listed so the usage view shows it.
func promptCachePeers(ctx context.Context, registry *nodes.NodeRegistry) ([]promptcachesvc.Peer, error) {
	list, err := registry.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []promptcachesvc.Peer
	for _, n := range list {
		if n.NodeType != "" && n.NodeType != nodes.NodeTypeBackend {
			continue
		}
		if n.Status == nodes.StatusPending {
			continue
		}
		out = append(out, promptcachesvc.Peer{
			ID: n.ID, Name: n.Name, Incarnation: n.WorkerIncarnation,
			Online: n.Status != nodes.StatusOffline,
		})
	}
	return out, nil
}

// kickPromptCache asks for a pass of the worker sync now, after a change of
// the cluster settings. Without distributed mode it does nothing.
func (a *Application) kickPromptCache() {
	a.promptCacheMu.RLock()
	d := a.promptCacheDist
	a.promptCacheMu.RUnlock()
	if d != nil {
		d.Kick()
	}
}

// PromptCacheEnabled reports whether the disk tier is on: in distributed mode
// the cluster switch (each worker may still be off on its own, which its
// report says), else this node.
func (a *Application) PromptCacheEnabled() bool {
	a.promptCacheMu.RLock()
	distributed := a.promptCacheDist != nil
	a.promptCacheMu.RUnlock()
	if distributed {
		return !a.ApplicationConfig().PromptCache.DiskDisabled
	}
	return a.PromptCacheNode().Enabled()
}

// PromptCacheNode is the disk tier of this node, nil before start.
func (a *Application) PromptCacheNode() *promptcachesvc.Node {
	a.promptCacheMu.RLock()
	defer a.promptCacheMu.RUnlock()
	return a.promptCache
}

// PromptCacheNodes is where purges and usage requests of the disk tier go.
func (a *Application) PromptCacheNodes() promptcachesvc.Nodes {
	a.promptCacheMu.RLock()
	defer a.promptCacheMu.RUnlock()
	if a.promptCacheNodes == nil {
		return promptcachesvc.Local{}
	}
	return a.promptCacheNodes
}

// SetPromptCacheNodes replaces the purge and usage seam, for a mode that
// reaches other nodes.
func (a *Application) SetPromptCacheNodes(n promptcachesvc.Nodes) {
	a.promptCacheMu.Lock()
	defer a.promptCacheMu.Unlock()
	a.promptCacheNodes = n
}

// promptCacheModelTTLs maps the model16 id of each model that sets
// prompt_cache_disk_ttl_hours to its time to live.
func (a *Application) promptCacheModelTTLs() map[string]time.Duration {
	cl := a.ModelConfigLoader()
	if cl == nil {
		return nil
	}
	out := map[string]time.Duration{}
	for _, c := range cl.GetAllModelsConfigs() {
		if c.Model == "" {
			continue
		}
		if ttl, ok := config.PromptCacheModelTTL(c.Options); ok {
			out[promptcache.ModelID(c.Model)] = ttl
		}
	}
	return out
}

// promptCacheUsers reports whether authentication is on and how many users
// it has, for the automatic per-user quota.
func (a *Application) promptCacheUsers() (bool, int) {
	db := a.AuthDB()
	if db == nil {
		return false, 0
	}
	var n int64
	if err := db.Model(&auth.User{}).Count(&n).Error; err != nil {
		return true, 0
	}
	return true, int(n)
}
