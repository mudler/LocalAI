package application

import (
	"context"
	"errors"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	addonendpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonupdates "github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

// upgradeRunsDBFile is the SQLite file of the upgrade runs of a server with no
// shared database, under the data path.
const upgradeRunsDBFile = "upgrade_runs.db"

// UpgradeRuns returns the service of the server-owned upgrade runs, or nil
// when it could not start (no database to keep the runs in). The routes that
// update one item fall back to their direct path then.
func (a *Application) UpgradeRuns() *upgraderun.Service { return a.upgradeRuns }

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
	if a.upgradeRunTweak != nil {
		a.upgradeRunTweak(&opts)
	}
	svc, err := upgraderun.New(opts)
	if err != nil {
		closeDB()
		xlog.Warn("Upgrade runs are not available", "error", err)
		return
	}
	ctx, stop := context.WithCancel(a.applicationConfig.Context)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	a.upgradeRuns = svc
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
		upgraderun.KindAddon: upgraderun.AddonStarter{Upgrader: a.addonRunUpgrader},
	}
	if lib := a.BackendLibrary(); lib != nil {
		starters[upgraderun.KindLibrary] = libraryRunStarter{installer: lib, status: gs}
	}
	checkers := []upgraderun.Checker{
		upgraderun.BackendChecker{Snapshots: backendSnapshots{a}},
		upgraderun.ModelChecker{Snapshots: modelSnapshots{a}},
	}
	if m := a.addonManager; m != nil && !cfg.Addons.IsFrontend() {
		// Only the process of the add-on owner lists the add-ons with an
		// update: the list needs the owner's own check, without an actor.
		checkers = append(checkers, addonRunChecker{src: m})
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
	return opts
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
// does for POST /backends/library/install.
type libraryRunStarter struct {
	installer upgraderun.LibraryInstaller
	status    upgraderun.StatusWriter
}

func (s libraryRunStarter) Start(ctx context.Context, actor string, it upgraderun.Item) (string, error) {
	in := s.installer
	if a, _, ok := addonendpoints.DecodeRunActor(actor); ok {
		in = actorInstaller{in: s.installer, actor: a}
	}
	return upgraderun.LibraryStarter{Installer: in, Status: s.status}.Start(ctx, actor, it)
}

type actorInstaller struct {
	in    upgraderun.LibraryInstaller
	actor contract.Actor
}

func (i actorInstaller) Install(ctx context.Context, sourceID, backend, version string, progress backendlib.Progress) error {
	return i.in.Install(library.WithActor(ctx, i.actor), sourceID, backend, version, progress)
}

// addonUpdateSource is the owner's list of add-on update statuses
// (addons.Manager implements it).
type addonUpdateSource interface {
	UpdateStatuses(ctx context.Context, fresh bool) ([]addonupdates.Status, bool, error)
}

// addonRunChecker lists the add-ons with an update for the upgrade runs.
type addonRunChecker struct{ src addonUpdateSource }

func (addonRunChecker) Kinds() []upgraderun.Kind { return []upgraderun.Kind{upgraderun.KindAddon} }

func (c addonRunChecker) List(ctx context.Context, fresh bool) (upgraderun.Listing, error) {
	sts, complete, err := c.src.UpdateStatuses(ctx, fresh)
	if err != nil {
		return upgraderun.Listing{}, err
	}
	l := upgraderun.Listing{Complete: complete}
	for _, st := range sts {
		if st.Throttled {
			l.Throttled = true
		}
		if st.State != addonupdates.UpdateAvailable || st.InstalledDigest == "" || st.CandidateDigest == "" {
			continue
		}
		l.Candidates = append(l.Candidates, upgraderun.Candidate{
			Kind: upgraderun.KindAddon, Name: st.AddonID,
			From: st.InstalledDigest, To: st.CandidateDigest, Risks: []string{},
			Extra: map[string]string{
				upgraderun.ExtraExpectedDigest:  st.InstalledDigest,
				upgraderun.ExtraCandidateDigest: st.CandidateDigest,
			},
		})
	}
	return l, nil
}
