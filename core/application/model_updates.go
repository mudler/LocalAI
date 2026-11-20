package application

import (
	"context"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/updatenotify"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// modelUpdateNotifier writes the model digest through the update emitter.
type modelUpdateNotifier struct{ em *updatenotify.Emitter }

func (n modelUpdateNotifier) ModelUpdates(ctx context.Context, items []modelupdates.Item) error {
	out := make([]updatenotify.ModelUpdate, len(items))
	for i, it := range items {
		out[i] = updatenotify.ModelUpdate{Name: it.Name, State: it.State}
	}
	return n.em.ModelDigest(ctx, out)
}

// startModelUpdates builds the update check of gallery models and starts its
// periodic check. In distributed mode the check takes the advisory lock
// KeyModelUpdateCheck, so one frontend checks at a time; its result is a file
// in the shared models directory that every frontend reads. Standalone, a
// process lock is enough.
func (a *Application) startModelUpdates() {
	options := a.ApplicationConfig()
	listGallery := func(ctx context.Context, fresh bool) ([]*gallery.GalleryModel, error) {
		// The listing has no context of its own: do not start it for a caller
		// that has gone, and do not hand it to one that went meanwhile.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fresh {
			gallery.ForgetGalleryIndexes()
		}
		// Read at each call: the galleries can change at runtime.
		models, err := gallery.AvailableGalleryModels(a.ApplicationConfig().Galleries, options.SystemState)
		if err == nil {
			err = ctx.Err()
		}
		return models, err
	}
	var clusterRT *clusterRuntime
	upgrader := &modelupdates.Upgrader{
		SystemState: options.SystemState,
		ListGallery: listGallery,
		Runtime:     &modelRuntime{app: a},
		Mutate:      a.ModelConfigLoader().WithModelConfigMutation,
		Instance:    options.Distributed.InstanceID,
		Notifier:    modelUpgradeNotifier{em: a.updateEmitter()},
	}
	if ds := a.distributed; ds != nil && ds.Registry != nil {
		crt := newClusterRuntime(&modelRuntime{app: a}, ds.Registry, a.ModelLoader())
		crt.revisions = a.ModelRevisionLifecycle()
		crt.revisionOf = func(name string) (string, error) {
			return a.ModelConfigLoader().RevisionFor(name, a.ApplicationConfig())
		}
		crt.storedRevision = ds.Registry.GetModelConfigRevision
		upgrader.Runtime = crt
		clusterRT = crt
		upgrader.StrategyNote = clusterStrategyNote(ds.Registry)
		// With one shared models directory the workers load the frontend's
		// files from it: there is nothing to move.
		if !options.Distributed.SharedModels && ds.Nats != nil {
			files := &nodes.UpgradeFiles{Nodes: ds.Registry, Sender: nodes.NATSModelFilePrefetcher{Bus: ds.Nats}}
			if ds.FileMgr != nil && ds.FileMgr.IsConfigured() {
				files.Objects = ds.FileMgr
			}
			upgrader.Workers = upgradeWorkers{files: files}
		}
	}
	if gs := a.GalleryService(); gs != nil {
		upgrader.BackendAvailable = func(name string) (bool, error) {
			bm := gs.BackendManager()
			if bm == nil {
				return true, nil
			}
			return bm.BackendAvailable(name)
		}
		gs.SetModelUpgrader(modelUpgraderOp{u: upgrader})
	}
	// The service is built below; it exists by the time a person changes a
	// hold or an upgrade ends.
	upgrader.Changed = func(string) {
		if svc := a.modelUpdates; svc != nil {
			svc.Notify(options.Context)
		}
	}
	if db := a.distributedDB(); db != nil {
		// One install, upgrade, hold or removal of a model at a time across
		// the cluster; the lock also tells a live journal from an abandoned
		// one (runJournalRecovery).
		upgrader.Lock = modelLockFor(db)
		if gs := a.GalleryService(); gs != nil {
			gs.SetModelLock(upgrader.Lock)
		}
	}
	a.modelUpgrader = upgrader
	opts := modelupdates.Options{
		SystemState: options.SystemState,
		ListGallery: listGallery,
		Notifier:    modelUpdateNotifier{em: a.updateEmitter()},
	}
	if db := a.distributedDB(); db != nil {
		opts.Lock = func(ctx context.Context, fn func() error) (bool, error) {
			return advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyModelUpdateCheck, fn)
		}
	}
	a.modelUpdates = modelupdates.New(opts)
	go a.modelUpdates.Run(options.Context)
	if upgrader.Lock != nil {
		var after func(context.Context)
		if clusterRT != nil {
			// A frontend that stopped during an upgrade leaves the block
			// revision in the registry; lift it for the models nobody
			// upgrades now.
			after = func(ctx context.Context) {
				var names []string
				for _, c := range a.ModelConfigLoader().GetAllModelsConfigs() {
					names = append(names, c.Name)
				}
				if done := clusterRT.RepublishBlocked(ctx, names, upgrader.Lock); len(done) > 0 {
					xlog.Info("Lifted the upgrade block of models in the registry", "models", done)
				}
			}
		}
		go runJournalRecovery(options.Context, upgrader, after)
	}
}

// modelUpgradeNotifier writes the result of an upgrade through the update
// emitter.
type modelUpgradeNotifier struct{ em *updatenotify.Emitter }

func (n modelUpgradeNotifier) ModelUpgradeResult(ctx context.Context, r modelupdates.UpgradeResult) error {
	return n.em.ModelUpgradeResult(ctx, updatenotify.ModelUpgrade{
		OpID: r.OpID, Name: r.Name, Failed: r.Failed, RolledBack: r.RolledBack, From: r.From, To: r.To, Err: r.Err,
	})
}

func (n modelUpgradeNotifier) ModelRemovedOld(ctx context.Context, key, name string, files int) error {
	return n.em.ModelRemovedOld(ctx, key, name, files)
}

// modelUpgraderOp runs a gallery operation's model upgrade on the upgrader.
type modelUpgraderOp struct{ u *modelupdates.Upgrader }

func (m modelUpgraderOp) UpgradeModel(ctx context.Context, opID, name string, opts galleryop.ModelUpgradeOptions, progress func(*galleryop.OpStatus)) error {
	return m.u.Upgrade(ctx, modelupdates.Request{
		OpID: opID, Name: name, ConfigOnly: opts.ConfigOnly, Choices: opts.Choices, Retry: opts.Retry,
		Progress: func(p modelupdates.Progress) {
			progress(&galleryop.OpStatus{
				Phase: p.Phase, Message: p.Message, FileName: p.FileName, Progress: p.Percent,
				TotalFileSize: p.Total, DownloadedFileSize: p.Current,
				// Until the model stops, a cancel leaves the old version as
				// it was; from then on the upgrade runs to its end.
				Cancellable: slices.Contains([]string{gallery.PhasePreflight, gallery.PhaseStaging}, p.Phase),
			})
		},
	})
}

// modelRuntime is the upgrade's view of the model runtime: stop and start.
// Standalone the model loader runs the model in this process. In distributed
// mode the loader routes the load to a worker through the SmartRouter
// (clusterRuntime); the strategy of a cluster upgrade (stop and start, a
// canary later) plugs in here.
type modelRuntime struct{ app *Application }

func (r *modelRuntime) IsLoaded(_ context.Context, name string) bool {
	return isLoadedIn(r.app.ModelLoader().ListLoadedModels(), name)
}

// isLoadedIn reports whether a model of this process has the ID name.
func isLoadedIn(models []*model.Model, name string) bool {
	return slices.ContainsFunc(models, func(m *model.Model) bool { return m.ID == name })
}

func (r *modelRuntime) Unload(_ context.Context, name string) error {
	return r.app.ModelLoader().ShutdownModel(name)
}

// BlockLoads refuses the loads of the model in this process while the upgrade
// switches its files.
func (r *modelRuntime) BlockLoads(_ context.Context, name string) error {
	r.app.ModelLoader().BlockLoads(name)
	return nil
}

func (r *modelRuntime) UnblockLoads(_ context.Context, name string) error {
	r.app.ModelLoader().UnblockLoads(name)
	return nil
}

func (r *modelRuntime) Reload(ctx context.Context, name string) error {
	opts := r.app.ApplicationConfig()
	if err := modeladmin.ApplyRemoteChange(ctx, r.app.ModelConfigLoader(), opts.SystemState.Model.ModelsPath,
		messaging.CacheInvalidateEvent{Element: name, Op: "upgrade"}, r.app.ModelRevisionLifecycle(), opts.ToConfigLoaderOptions()...); err != nil {
		return err
	}
	if gs := r.app.GalleryService(); gs != nil {
		gs.BroadcastModelsChanged(name, "upgrade")
	}
	return nil
}

func (r *modelRuntime) Load(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, err := backend.PreloadModelByName(ctx, r.app.ModelConfigLoader(), r.app.ModelLoader(), r.app.ApplicationConfig(), name)
	return err
}

// ModelUpgrader returns the runner of model upgrades, or nil when no model
// gallery is configured.
func (a *Application) ModelUpgrader() *modelupdates.Upgrader {
	return a.modelUpgrader
}
