package application

import (
	"path/filepath"

	"github.com/mudler/xlog"

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
// opened, like initChatHistoryStore.
func (a *Application) initStudioStore() {
	var (
		s    store.Store
		kind string
		err  error
	)
	switch {
	case a.authDB != nil:
		s, err = store.NewGORM(a.authDB)
		kind = "gorm"
	case a.applicationConfig.DataPath != "":
		s, err = store.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats"))
		kind = "json"
	default:
		s, err = store.NewJSON("")
		kind = "memory"
	}
	if err != nil {
		xlog.Warn("Chats disabled: failed to initialise its store", "kind", kind, "error", err)
		return
	}
	a.studioStore, a.studioStoreKind = s, kind
}
