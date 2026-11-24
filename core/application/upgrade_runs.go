package application

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	addonendpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/updatenotify"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/xlog"
)

// upgradeRunsDBFile is the SQLite file of the upgrade runs of a server with no
// shared database, under the data path.
const upgradeRunsDBFile = "upgrade_runs.db"

// UpgradeRuns returns the service of the server-owned upgrade runs, or nil
// when it could not start (no database to keep the runs in). The routes that
// update one item fall back to their direct path then.
func (a *Application) UpgradeRuns() *upgraderun.Service { return a.upgradeRuns.Load() }

// AddonRunUpgrader starts the add-on items of upgrade runs; the add-on upgrade
// route hands its recorded upgrade to it. Never nil.
func (a *Application) AddonRunUpgrader() *addonendpoints.QueuedUpgrader {
	return a.addonRunUpgrader
}

// SetOperationCache gives the upgrade runs the cache of /api/operations, so the
// operations they start show on the rows of the items. The HTTP layer builds
// the cache after the application.
func (a *Application) SetOperationCache(c *galleryop.OpCache) { a.opCache.Store(c) }

func (a *Application) operationCache() *galleryop.OpCache { return a.opCache.Load() }

// upgradeRunsDB is the database of the runs: the shared database in a cluster
// or when authentication keeps one, otherwise a SQLite file of their own under
// the data path. The second result closes a database opened here.
func (a *Application) upgradeRunsDB() (*gorm.DB, func(), error) {
	if a.authDB != nil {
		return a.authDB, func() {}, nil
	}
	dataPath := a.applicationConfig.DataPath
	if dataPath == "" {
		return nil, nil, errors.New("no database and no data path to keep the runs in")
	}
	db, err := auth.OpenSQLiteFile(filepath.Join(dataPath, upgradeRunsDBFile))
	if err != nil {
		return nil, nil, err
	}
	return db, func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}, nil
}

// startUpgradeRuns builds the upgrade run service and starts the sweep that
// takes over the runs nobody runs. Distributed mode: shared state (the run
// rows in the shared database) and a single runner per run (an advisory lock
// of the run); see docs/content/models/upgrade-runs.md. Standalone it keeps
// the runs in its own database, so they survive a restart.
//
// A failure to start is logged and leaves the service nil: the single-item
// upgrade routes then keep their direct path.
func (a *Application) startUpgradeRuns() {
	a.addonRunUpgrader = &addonendpoints.QueuedUpgrader{
		Gateway: func() *gateway.Gateway { return a.addonGateway },
		Queue:   a.GalleryService,
		OpCache: a.operationCache,
	}
	gs := a.GalleryService()
	if gs == nil {
		return
	}
	var store *distributed.UpgradeRunStore
	var bus messaging.Broadcaster
	var toucher upgraderun.OperationToucher
	closeDB := func() {}
	if d := a.distributed; d != nil && d.DistStores != nil && d.DistStores.UpgradeRuns != nil {
		store = d.DistStores.UpgradeRuns
		bus = d.Broadcaster
		if d.DistStores.Gallery != nil {
			toucher = d.DistStores.Gallery
		}
	} else {
		db, closer, err := a.upgradeRunsDB()
		if err != nil {
			xlog.Warn("Upgrade runs are not available", "error", err)
			return
		}
		s, err := distributed.NewUpgradeRunStore(db)
		if err != nil {
			closer()
			xlog.Warn("Upgrade runs are not available: could not open their store", "error", err)
			return
		}
		store, closeDB = s, closer
	}
	opts := a.upgradeRunOptions(store, bus, toucher)
	// The observers are part of the options, so they hear the first change
	// of a run that the sweep takes over as soon as the service runs: one
	// live row and one end row for each run in the inbox of each
	// administrator, written by the frontend that runs the run, and the
	// drop of an add-on upgrade that a run recorded and never started.
	opts.Observers = append(opts.Observers,
		&updatenotify.RunNotifier{Emitter: a.updateEmitter(), Context: a.applicationConfig.Context},
		a.addonRunUpgrader)
	if a.upgradeRunTweak != nil {
		a.upgradeRunTweak(&opts)
	}
	svc, err := upgraderun.New(opts)
	if err != nil {
		closeDB()
		xlog.Warn("Upgrade runs are not available", "error", err)
		return
	}
	a.upgradeRuns.Store(svc)
	ctx, stop := context.WithCancel(a.applicationConfig.Context)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	a.upgradeRunsStop = func() {
		stop()
		<-done
		svc.Close()
		closeDB()
	}
}

// stopUpgradeRuns stops the runners of this frontend. Their runs stay running
// in the store: this frontend after a restart, or another one, takes them
// over.
func (a *Application) stopUpgradeRuns() {
	// First the retries of the automatic passes: they start runs on the
	// service that stops below.
	a.autoRunner().Close()
	if a.upgradeRunsStop != nil {
		a.upgradeRunsStop()
		a.upgradeRunsStop = nil
	}
}

func (a *Application) upgradeRunOptions(store *distributed.UpgradeRunStore, bus messaging.Broadcaster, toucher upgraderun.OperationToucher) upgraderun.Options {
	gs := a.GalleryService()
	cfg := a.applicationConfig
	starters := map[upgraderun.Kind]upgraderun.Starter{
		upgraderun.KindGallery: upgraderun.GalleryBackendStarter{
			Gallery:   gs,
			OpCache:   opCacheSetter{a},
			Galleries: func() []config.Gallery { return a.ApplicationConfig().BackendGalleries },
		},
		upgraderun.KindModel: upgraderun.ModelStarter{
			Gallery:  gs,
			Validate: upgraderun.ModelValidator(a.ModelUpgrader, a.ModelUpdates),
		},
		upgraderun.KindAddon: autoAddonStarter{
			inner:   upgraderun.AddonStarter{Upgrader: a.addonRunUpgrader},
			manager: func() addonAutoUpgrader { return a.autoUpgrader() },
			queue:   func() addonOpQueue { return a.queueOf() },
			opCache: a.operationCache,
		},
	}
	if a.BackendLibrary() != nil {
		starters[upgraderun.KindLibrary] = libraryRunStarter{jobs: a.LibraryJobs, status: gs}
	}
	checkers := []upgraderun.Checker{
		upgraderun.BackendChecker{Snapshots: backendSnapshots{a}},
		upgraderun.ModelChecker{Snapshots: modelSnapshots{a}},
	}
	if cfg.Addons.Enabled {
		// A run of an administrator lists the add-ons through the gateway, as
		// the actor of the run: the owner answers on its own process and
		// every other frontend forwards to it. A run of the automatic update
		// lists them on the owner's process only, with the owner's own check:
		// its system actor never reaches the gateway.
		var owner ownerStatusSource
		if m := a.addonManager; m != nil {
			owner = m
		}
		checkers = append(checkers, addonCheckerBySource{
			manual: addonendpoints.GatewayUpdateChecker{Gateway: func() *gateway.Gateway { return a.addonGateway }},
			auto:   ownerAddonChecker{src: owner, owner: func() bool { return !cfg.Addons.IsFrontend() }},
			isAuto: isAutoRunCtx,
		})
	}
	opts := upgraderun.Options{
		Store:    store,
		Bus:      bus,
		Instance: cfg.Distributed.InstanceID,
		Jobs:     gs,
		Starters: starters,
		Checkers: checkers,
		Config: func() upgraderun.Config {
			return upgraderun.Config{BackendLimit: a.ApplicationConfig().BackendOperationLimit(), ModelLimit: 1}
		},
	}
	if toucher != nil {
		opts.Toucher = toucher
	}
	opts.Retention = upgradeRunRetention(cfg.UpgradeRunKeep, cfg.UpgradeRunRetentionDays)
	return opts
}

// upgradeRunRetention reads the retention settings: zero is the default, a
// negative value turns that limit off.
func upgradeRunRetention(keep, days int) *upgraderun.Retention {
	r := upgraderun.DefaultRetention
	switch {
	case keep < 0:
		r.Keep = -1
	case keep > 0:
		r.Keep = keep
	}
	switch {
	case days < 0:
		r.MaxAge = 0
	case days > 0:
		r.MaxAge = time.Duration(days) * 24 * time.Hour
	}
	return &r
}

// opCacheSetter registers a backend operation in the cache of /api/operations
// once the HTTP layer gave it one.
type opCacheSetter struct{ a *Application }

func (o opCacheSetter) SetBackend(key, value string) {
	if c := o.a.operationCache(); c != nil {
		c.SetBackend(key, value)
	}
}

// backendSnapshots reads the backend update check, which is nil when backend
// installs are off or no backend gallery is configured.
type backendSnapshots struct{ a *Application }

func (b backendSnapshots) UpdatesSnapshot(ctx context.Context) backendupdates.Snapshot {
	if uc := b.a.UpgradeChecker(); uc != nil {
		return uc.UpdatesSnapshot(ctx)
	}
	return backendupdates.Snapshot{}
}

func (b backendSnapshots) CheckNow(ctx context.Context) backendupdates.Snapshot {
	if uc := b.a.UpgradeChecker(); uc != nil {
		return uc.CheckNow(ctx)
	}
	return backendupdates.Snapshot{}
}

// modelSnapshots reads the model update check, which is nil without model
// galleries.
type modelSnapshots struct{ a *Application }

func (m modelSnapshots) Snapshot(ctx context.Context) modelupdates.Snapshot {
	if s := m.a.ModelUpdates(); s != nil {
		return s.Snapshot(ctx)
	}
	return modelupdates.Snapshot{}
}

func (m modelSnapshots) CheckNow(ctx context.Context) modelupdates.Snapshot {
	if s := m.a.ModelUpdates(); s != nil {
		return s.CheckNow(ctx)
	}
	return modelupdates.Snapshot{}
}

// libraryRunStarter installs a library backend for the actor of the run: on a
// frontend the add-on owner resolves the entry and checks that actor, as it
// does for POST /backends/library/install. It uses the job runner of the
// route, so the run and the route never install one backend name at once.
type libraryRunStarter struct {
	jobs   func() *backendlib.JobRunner
	status backendlib.JobStatus
}

func (s libraryRunStarter) Start(ctx context.Context, actor string, it upgraderun.Item) (string, error) {
	if a, _, ok := addonendpoints.DecodeRunActor(actor); ok {
		ctx = library.WithActor(ctx, a)
	}
	var jobs upgraderun.LibraryJobs
	if r := s.jobs(); r != nil {
		jobs = r
	}
	return upgraderun.LibraryStarter{Jobs: jobs, Status: s.status}.Start(ctx, actor, it)
}

// autoUpgrader is the add-on owner for the automatic upgrade, or nil.
func (a *Application) autoUpgrader() addonAutoUpgrader {
	if m := a.addonManager; m != nil {
		return m
	}
	return nil
}

// queueOf is the operation queue for add-on operations, or nil.
func (a *Application) queueOf() addonOpQueue {
	if gs := a.GalleryService(); gs != nil {
		return gs
	}
	return nil
}
