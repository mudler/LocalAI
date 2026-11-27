package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/promptcache"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/xlog"
)

// startPromptCache starts the disk tier of the prompt cache of this node: the
// root under the data path, the read-speed probe, the janitor loop, the
// environment of the backend processes, and the purge of the namespaces of a
// removed user or client key.
//
// Multi-frontend: the files live on the local disk of the node that runs the
// backend, so the state is per-instance. Each node runs its own janitor;
// a purge through PromptCacheNodes reaches the nodes its implementation knows
// (Local: this node only).
func (a *Application) startPromptCache(o *config.ApplicationConfig) {
	node := promptcachesvc.NewNode(o.PromptCacheRoot(), o.SystemState.Model.ModelsPath,
		func() config.PromptCacheConfig { return o.PromptCache },
		promptcachesvc.WithModelTTLs(a.promptCacheModelTTLs),
		promptcachesvc.WithUsers(a.promptCacheUsers),
	)
	a.promptCacheMu.Lock()
	a.promptCache = node
	if a.promptCacheNodes == nil {
		a.promptCacheNodes = promptcachesvc.Local{Node: node}
	}
	a.promptCacheMu.Unlock()

	a.ModelLoader().SetBackendEnv(node.BackendEnv)
	o.PromptCacheAutoQuota = func() int64 { return node.Policy().QuotaBytes }
	node.Start(o.Context)

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
