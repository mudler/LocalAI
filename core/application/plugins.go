package application

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/internal"
)

// Plugins returns the service of the plugins in the Claude Code plugin
// format, or nil when it is off; PluginsUnavailable says why.
func (a *Application) Plugins() *plugins.Service { return a.pluginsService.Load() }

// PluginsUnavailable returns why the Plugins service is off, or "".
func (a *Application) PluginsUnavailable() string {
	if a.pluginsService.Load() != nil {
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
		ID: j.ID, PluginID: j.PluginID, Update: j.Update, By: j.By,
		Run: func(ctx context.Context, progress func(*galleryop.OpStatus)) error {
			progress(&galleryop.OpStatus{Message: "installing", Phase: "installing", Cancellable: true})
			return j.Run(ctx)
		},
	})
}

// newPluginsService builds the Plugins service. It reads the settings of cfg
// at each request. A nil queue runs the jobs at once.
func newPluginsService(cfg *config.ApplicationConfig, db *gorm.DB, ws workspace.Store, queue plugins.Queue, conns plugins.ConnectorSync, allowed func(context.Context, string) bool) (*plugins.Service, *fetch.Fetcher, error) {
	dp := cfg.DataPath
	st, err := pluginStoreFor(db, dp)
	if err != nil {
		return nil, nil, err
	}
	// The copy out of a checkout is capped at the highest allowed setting;
	// the tree check applies the configured one.
	copyLimits := content.DefaultLimits()
	copyLimits.MaxTotalBytes = config.MaxPluginMaxBytes
	// The switch of the MCP connectors for private addresses also covers the
	// plugin and marketplace locations: both make the server connect to an
	// address that a user or a marketplace chose.
	fetcher, err := fetch.New(fetch.Options{
		ScratchRoot: filepath.Join(dp, "plugins", "scratch"), Limits: copyLimits,
		RefusePrivate: func() bool { return !cfg.MCPConnectorsAllowPrivate },
	})
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
		Connectors:   conns,
		Allowed:      allowed,
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
	svc, fetcher, err := newPluginsService(cfg, a.authDB, ws, queue, a.pluginConnectors(), a.pluginsAllowed)
	if err != nil {
		return err
	}
	if err := fetcher.CleanScratch(); err != nil {
		xlog.Warn("plugins: remove old scratch folders", "error", err)
	}
	if err := svc.Seed(cfg.Context); err != nil {
		xlog.Warn("plugins: seed the default marketplaces", "error", err)
	}
	ctx, cancel := context.WithCancel(cfg.Context)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	a.pluginsStop, a.pluginsDone = cancel, done
	a.pluginsService.Store(svc)
	return nil
}

// latePlugins gives the chats the turn view of the Plugins service, which
// starts after the chats service. Before it starts, or when it is off, a
// turn has no plugins.
type latePlugins struct{ a *Application }

func (l latePlugins) TurnView(ctx context.Context, principal string, chat plugins.ChatRef) ([]plugins.ActivePlugin, error) {
	svc := l.a.Plugins()
	if svc == nil {
		return nil, nil
	}
	return svc.TurnView(ctx, principal, chat)
}

// pluginConnectors gives the Plugins service the MCP connectors of the
// chats, for the connectors that plugins own, or nil when the chats have
// none.
func (a *Application) pluginConnectors() plugins.ConnectorSync {
	if a.studioMCPConns == nil {
		return nil
	}
	return pluginConnectorSync{a.studioMCPConns}
}

// pluginConnectorSync adapts the MCP connectors service to the Plugins
// service.
type pluginConnectorSync struct{ s *mcpconns.Service }

func (p pluginConnectorSync) SyncPluginConnectors(ctx context.Context, principal, installID, plugin, digest string, servers []plugins.RemoteServer) error {
	return p.s.SyncPlugin(ctx, principal, installID, plugin, digest, plugins.ConnectorServers(servers))
}

func (p pluginConnectorSync) DropPluginConnectors(ctx context.Context, principal, installID string, all bool) error {
	return p.s.DropPlugin(ctx, principal, installID, all)
}

func (p pluginConnectorSync) ResyncPluginConnectors(ctx context.Context, installID, plugin, digest string, servers []plugins.RemoteServer) error {
	return p.s.ResyncPlugin(ctx, installID, plugin, digest, plugins.ConnectorServers(servers))
}

// pluginsAllowed reports whether a user holds the plugins feature, the rule
// of /api/plugins, for the plugins of a turn. Without accounts everyone
// does. A user that cannot be read gets no plugins.
func (a *Application) pluginsAllowed(ctx context.Context, principal string) bool {
	if a.authDB == nil {
		return true
	}
	u, err := a.loadStudioUser(ctx, principal)
	return err == nil && auth.HasFeatureAccess(a.authDB, u, auth.FeaturePlugins)
}
