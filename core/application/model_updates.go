package application

import (
	"context"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/core/services/updatenotify"
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
	opts := modelupdates.Options{
		SystemState: options.SystemState,
		ListGallery: func(_ context.Context, fresh bool) ([]*gallery.GalleryModel, error) {
			if fresh {
				gallery.ForgetGalleryIndexes()
			}
			// Read at each check: the galleries can change at runtime.
			return gallery.AvailableGalleryModels(a.ApplicationConfig().Galleries, options.SystemState)
		},
		Notifier: modelUpdateNotifier{em: a.updateEmitter()},
	}
	if db := a.distributedDB(); db != nil {
		opts.Lock = func(ctx context.Context, fn func() error) (bool, error) {
			return advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyModelUpdateCheck, fn)
		}
	}
	a.modelUpdates = modelupdates.New(opts)
	go a.modelUpdates.Run(options.Context)
}
