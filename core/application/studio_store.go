package application

import (
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// StudioProvider returns the studio loop's model provider. Its transport gets
// the HTTP router once the server has built it, so the loop reaches the model
// through the same chat route as any client, in memory.
func (a *Application) StudioProvider() *provider.Provider {
	return a.studioProvider
}

// StudioStore returns the store of the studio loop. It is never nil after New
// succeeds: it is GORM on the auth database when there is one, JSON files under
// the data path when there is not, and memory only when there is no data path.
// A cluster has PostgreSQL, so it always gets the GORM store; the JSON store is
// for one process.
func (a *Application) StudioStore() store.Store {
	return a.studioStore
}

// initStudioStore picks the store. It must run after the auth database is
// opened.
func (a *Application) initStudioStore() {
	var (
		s    store.Store
		ag   agents.Store
		nt   notify.Store
		ps   notify.PushStore
		cs   connectors.Store
		kind string
		err  error
	)
	switch {
	case a.authDB != nil:
		if s, err = store.NewGORM(a.authDB); err == nil {
			if ag, err = agents.NewGORM(a.authDB); err == nil {
				if nt, err = notify.NewGORM(a.authDB); err == nil {
					if ps, err = notify.NewGORMPush(a.authDB); err == nil {
						cs, err = connectors.NewGORM(a.authDB)
					}
				}
			}
		}
		kind = "gorm"
	case a.applicationConfig.DataPath != "":
		dir := filepath.Join(a.applicationConfig.DataPath, "chats")
		if s, err = store.NewJSON(dir); err == nil {
			if ag, err = agents.NewJSON(filepath.Join(dir, "agents")); err == nil {
				if nt, err = notify.NewJSON(filepath.Join(dir, "notifications")); err == nil {
					if ps, err = notify.NewJSONPush(filepath.Join(dir, "push")); err == nil {
						cs, err = connectors.NewJSON(filepath.Join(dir, "connectors"))
					}
				}
			}
		}
		kind = "json"
	default:
		if s, err = store.NewJSON(""); err == nil {
			if ag, err = agents.NewJSON(""); err == nil {
				if nt, err = notify.NewJSON(""); err == nil {
					cs, err = connectors.NewJSON("")
				}
			}
		}
		kind = "memory"
	}
	if err != nil {
		xlog.Warn("Chats disabled: failed to initialise its store", "kind", kind, "error", err)
		return
	}
	a.studioStore, a.studioAgents, a.studioNotify, a.studioPush, a.studioConnStore, a.studioStoreKind = s, ag, nt, ps, cs, kind
}
