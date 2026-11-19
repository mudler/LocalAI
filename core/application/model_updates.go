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
	"github.com/mudler/LocalAI/core/services/updatenotify"
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
	listGallery := func(_ context.Context, fresh bool) ([]*gallery.GalleryModel, error) {
		if fresh {
			gallery.ForgetGalleryIndexes()
		}
		// Read at each call: the galleries can change at runtime.
		return gallery.AvailableGalleryModels(a.ApplicationConfig().Galleries, options.SystemState)
	}
	upgrader := &modelupdates.Upgrader{
		SystemState: options.SystemState,
		ListGallery: listGallery,
		Runtime:     &modelRuntime{app: a},
		Mutate:      a.ModelConfigLoader().WithModelConfigMutation,
		Instance:    options.Distributed.InstanceID,
		Notifier:    modelUpgradeNotifier{em: a.updateEmitter()},
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
	a.modelUpgrader = upgrader
	policy := journalPolicy(options)
	opts := modelupdates.Options{
		SystemState: options.SystemState,
		ListGallery: listGallery,
		Notifier:    modelUpdateNotifier{em: a.updateEmitter()},
		// The runner of a check also ends the upgrades whose frontend
		// stopped: a stale journal is not touched by anyone else.
		AfterTick: func(ctx context.Context) {
			stale := func(j *gallery.Journal) bool { return j.Instance != options.Distributed.InstanceID && policy(j) }
			if names, err := upgrader.Recover(ctx, stale); err != nil {
				xlog.Warn("Could not end some interrupted model upgrades", "error", err)
			} else if len(names) > 0 {
				xlog.Info("Ended interrupted model upgrades", "models", names)
			}
		},
	}
	if db := a.distributedDB(); db != nil {
		opts.Lock = func(ctx context.Context, fn func() error) (bool, error) {
			return advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyModelUpdateCheck, fn)
		}
	}
	a.modelUpdates = modelupdates.New(opts)
	go a.modelUpdates.Run(options.Context)
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
// mode the loader routes the load to a worker through the SmartRouter, and
// Reload advances the config revision, which retires the replicas of the old
// one; the strategy of a cluster upgrade (stop and start, a canary later)
// plugs in here.
type modelRuntime struct{ app *Application }

func (r *modelRuntime) IsLoaded(name string) bool {
	for _, m := range r.app.ModelLoader().ListLoadedModels() {
		if m.ID == name {
			return true
		}
	}
	return false
}

func (r *modelRuntime) Unload(_ context.Context, name string) error {
	return r.app.ModelLoader().ShutdownModel(name)
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
