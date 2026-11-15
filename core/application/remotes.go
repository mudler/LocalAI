package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/remotes"
)

// Remotes returns the remotes service, or nil when its store
// could not be opened (the reason is in the startup log). Callers must handle
// nil. The service keeps no state in memory: every frontend of a cluster reads
// the shared store.
func (a *Application) Remotes() remotes.Service { return a.remotes }

// SetRemotes replaces the remotes service. Startup sets it; this is
// for tests of the code that reads it.
func (a *Application) SetRemotes(s remotes.Service) { a.remotes = s }

// ModelRevisionLifecycle returns the lifecycle that applies a model config
// change: local shutdown in standalone mode, the node registry in a cluster.
// The model routes, the startup wiring and the remotes service share it.
func (a *Application) ModelRevisionLifecycle() modeladmin.ModelRevisionLifecycle {
	if a == nil {
		return nil
	}
	if a.distributed == nil {
		return modeladmin.NewLocalModelRevisionLifecycle(a.ModelLoader())
	}
	return modeladmin.NewDistributedModelRevisionLifecycle(a.distributed.Registry, a.distributed.ModelCleanup)
}

// startRemotes builds the remotes service, installs it as the source
// of the keys of models that carry proxy.remote, and refreshes every
// remote once in the background. It never fails the startup: without the
// service, such models fail to load with a clear error and nothing else is
// affected.
//
// Distributed mode keeps the records in the shared database, so any frontend
// sees them, and one frontend at a time runs the startup refresh. Standalone
// mode keeps them in a file under the data path.
func (a *Application) startRemotes(ctx context.Context) {
	var (
		store remotes.Store
		err   error
	)
	switch db := a.distributedDB(); {
	case db != nil:
		store, err = remotes.NewGORMStore(db)
	case a.applicationConfig.DataPath != "":
		store, err = remotes.NewJSONStore(filepath.Join(a.applicationConfig.DataPath, "remotes"))
	default:
		xlog.Warn("No data path is set: remotes are kept in memory and are lost at restart")
		store, err = remotes.NewJSONStore("")
	}
	if err != nil {
		xlog.Error("Remotes are unavailable: the store could not be opened", "error", err)
		return
	}
	admin := modeladmin.NewConfigService(a.ModelConfigLoader(), a.ApplicationConfig(), a.ModelRevisionLifecycle())
	opts := remotes.Options{
		Store:      store,
		Admin:      admin,
		Configs:    a.ModelConfigLoader(),
		Unloader:   a.ModelLoader(),
		ModelsPath: a.ApplicationConfig().SystemState.Model.ModelsPath,
	}
	if a.galleryService != nil {
		opts.Broadcaster = a.galleryService
	}
	svc, err := remotes.New(opts)
	if err != nil {
		xlog.Error("Remotes are unavailable", "error", err)
		return
	}
	a.remotes = svc
	// One resolver per process: the key of a model load comes from here.
	config.SetProxyKeyResolver(svc)

	// Shutdown cancels the refresh and waits for it, so no outbound call or
	// store write outlives the application.
	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.remotesStop = func() {
		cancel()
		<-done
	}
	go func() {
		defer close(done)
		remotes.StartupRefresh(refreshCtx, svc, a.distributedDB())
	}()
}
