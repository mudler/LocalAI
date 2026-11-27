package application

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/internal"
)

// Plugins returns the service of the plugins in the Claude Code plugin
// format, or nil when it is off; PluginsUnavailable says why.
func (a *Application) Plugins() *plugins.Service { return a.pluginsService }

// PluginsUnavailable returns why the Plugins service is off, or "".
func (a *Application) PluginsUnavailable() string {
	if a.pluginsService != nil {
		return ""
	}
	if a.pluginsReason == "" {
		return "Plugins are not set up on this server."
	}
	return a.pluginsReason
}

// pluginStoreFor picks the store of the plugin records: the shared database
// when there is one, otherwise a file under the data path (one frontend).
func pluginStoreFor(db *gorm.DB, dataPath string) (store.Store, error) {
	if db != nil {
		s, err := store.NewGormStore(db)
		if err != nil {
			return nil, fmt.Errorf("open the plugin tables: %w", err)
		}
		return s, nil
	}
	s, err := store.NewFileStore(filepath.Join(dataPath, "plugins", "store.json"))
	if err != nil {
		return nil, fmt.Errorf("open the plugin store file: %w", err)
	}
	return s, nil
}

// pluginQueue runs the plugin jobs in the operation queue, as add-on installs
// do: the job runs on this frontend and shows on every frontend.
type pluginQueue struct{ g *galleryop.GalleryService }

func (q pluginQueue) Enqueue(j plugins.Job) {
	q.g.EnqueuePluginOp(galleryop.PluginOp{
		ID: j.ID, PluginID: j.PluginID, Update: j.Update,
		Run: func(ctx context.Context, progress func(*galleryop.OpStatus)) error {
			progress(&galleryop.OpStatus{Message: "installing", Phase: "installing", Cancellable: true})
			return j.Run(ctx)
		},
	})
}

// newPluginsService builds the Plugins service. It reads the settings of cfg
// at each request. A nil queue runs the jobs at once.
func newPluginsService(cfg *config.ApplicationConfig, db *gorm.DB, ws workspace.Store, queue plugins.Queue) (*plugins.Service, *fetch.Fetcher, error) {
	dp := cfg.DataPath
	st, err := pluginStoreFor(db, dp)
	if err != nil {
		return nil, nil, err
	}
	// The copy out of a checkout is capped at the highest allowed setting;
	// the tree check applies the configured one.
	copyLimits := content.DefaultLimits()
	copyLimits.MaxTotalBytes = config.MaxPluginMaxBytes
	fetcher, err := fetch.New(fetch.Options{ScratchRoot: filepath.Join(dp, "plugins", "scratch"), Limits: copyLimits})
	if err != nil {
		return nil, nil, err
	}
	var defaults []plugins.DefaultMarketplace
	if list := cfg.Plugins.Marketplaces; list != "" {
		if defaults, err = plugins.ParseDefaults(list); err != nil {
			return nil, nil, fmt.Errorf("--plugin-marketplaces: %w", err)
		}
	}
	o := plugins.Options{
		Store: st, DB: db, Fetcher: fetcher, Root: files.PluginsPath, Defaults: defaults,
		Version: internal.Version, Queue: queue,
		Limits: func() content.Limits {
			l := content.DefaultLimits()
			l.MaxTotalBytes = cfg.EffectivePluginMaxBytes()
			return l
		},
		Official:     func() bool { return cfg.OfficialLibraries },
		UserInstalls: func() bool { return !cfg.Plugins.DisableUserInstalls },
	}
	if ws != nil {
		o.Workspace = content.Workspace{Files: ws, Actor: workspace.Actor{Principal: files.AdminPrincipal}}
	}
	svc, err := plugins.New(o)
	return svc, fetcher, err
}

// initPlugins starts the Plugins service: it removes the scratch folders a
// crash left, seeds the default marketplaces of a fresh store, and starts the
// refresh loop. It needs a data path (for the scratch folders, never /tmp)
// and the files of the chats (for the package files).
func (a *Application) initPlugins() error {
	cfg := a.applicationConfig
	if cfg.DataPath == "" {
		a.pluginsReason = "Plugins need a data path."
		return nil
	}
	ws := a.StudioFiles().Store()
	if ws == nil {
		a.pluginsReason = "Plugins need the files of the chats: " + a.StudioFiles().Unavailable()
		return nil
	}
	var queue plugins.Queue
	if a.galleryService != nil {
		queue = pluginQueue{a.galleryService}
	}
	svc, fetcher, err := newPluginsService(cfg, a.authDB, ws, queue)
	if err != nil {
		return err
	}
	if err := fetcher.CleanScratch(); err != nil {
		xlog.Warn("plugins: remove old scratch folders", "error", err)
	}
	if err := svc.Seed(cfg.Context); err != nil {
		xlog.Warn("plugins: seed the default marketplaces", "error", err)
	}
	go svc.Run(cfg.Context)
	a.pluginsService = svc
	return nil
}
