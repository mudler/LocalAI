package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/connections"
	"github.com/mudler/LocalAI/core/services/modeladmin"
)

// Connections returns the remote connections service, or nil when its store
// could not be opened (the reason is in the startup log). Callers must handle
// nil. The service keeps no state in memory: every frontend of a cluster reads
// the shared store.
func (a *Application) Connections() connections.Service { return a.connections }

// SetConnections replaces the connections service. Startup sets it; this is
// for tests of the code that reads it.
func (a *Application) SetConnections(s connections.Service) { a.connections = s }

// connectionsModelRevisionLifecycle returns the lifecycle that the model
// routes use: local shutdown in standalone mode, the registry in a cluster.
func (a *Application) connectionsModelRevisionLifecycle() modeladmin.ModelRevisionLifecycle {
	if a.distributed == nil {
		return modeladmin.NewLocalModelRevisionLifecycle(a.ModelLoader())
	}
	return modeladmin.NewDistributedModelRevisionLifecycle(a.distributed.Registry, a.distributed.ModelCleanup)
}

// startConnections builds the connections service, installs it as the source
// of the keys of models that carry proxy.connection, and refreshes every
// connection once in the background. It never fails the startup: without the
// service, such models fail to load with a clear error and nothing else is
// affected.
//
// Distributed mode keeps the records in the shared database, so any frontend
// sees them, and one frontend at a time runs the startup refresh. Standalone
// mode keeps them in a file under the data path.
func (a *Application) startConnections(ctx context.Context) {
	var (
		store connections.Store
		err   error
	)
	switch db := a.distributedDB(); {
	case db != nil:
		store, err = connections.NewGORMStore(db)
	case a.applicationConfig.DataPath != "":
		store, err = connections.NewJSONStore(filepath.Join(a.applicationConfig.DataPath, "connections"))
	default:
		xlog.Warn("No data path is set: remote connections are kept in memory and are lost at restart")
		store, err = connections.NewJSONStore("")
	}
	if err != nil {
		xlog.Error("Remote connections are unavailable: the store could not be opened", "error", err)
		return
	}
	admin := modeladmin.NewConfigService(a.ModelConfigLoader(), a.ApplicationConfig(), a.connectionsModelRevisionLifecycle())
	opts := connections.Options{
		Store:      store,
		Admin:      admin,
		Configs:    a.ModelConfigLoader(),
		Unloader:   a.ModelLoader(),
		ModelsPath: a.ApplicationConfig().SystemState.Model.ModelsPath,
	}
	if a.galleryService != nil {
		opts.Broadcaster = a.galleryService
	}
	svc, err := connections.New(opts)
	if err != nil {
		xlog.Error("Remote connections are unavailable", "error", err)
		return
	}
	a.connections = svc
	// One resolver per process: the key of a model load comes from here.
	config.SetProxyKeyResolver(svc)

	go connections.StartupRefresh(ctx, svc, a.distributedDB())
}
