package application

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/xlog"
)

// LibrarySources returns the one store of library sources this process works
// on. When the add-on owner runs here it is the owner's store, so a change made
// through the HTTP routes and the add-on catalog cannot overwrite each other;
// otherwise the store file under the data path is opened once. It is the file
// `local-ai library` edits.
func (a *Application) LibrarySources() (*catalog.Sources, error) {
	if a.AddonFrontend() {
		return nil, fmt.Errorf("%w: library sources are managed on the add-on owner", gateway.ErrUnavailable)
	}
	if a.addonSources != nil {
		return a.addonSources, nil
	}
	a.libraryStoreMu.Lock()
	defer a.libraryStoreMu.Unlock()
	if a.libraryStore != nil {
		return a.libraryStore, nil
	}
	s, err := catalog.OpenStore(catalog.StorePath(a.applicationConfig.DataPath))
	if err != nil {
		return nil, err
	}
	s.SetOfficial(a.applicationConfig.OfficialLibraries)
	a.libraryStore = s
	return s, nil
}

// ApplyLibraries makes the running server follow the library store: it sets
// the official-libraries switch, brings the model and backend galleries in line
// with the enabled sources and, for sources the add-on catalog reads, fetches
// those that just came back. dropped names the addresses of deleted sources.
// It is called after the HTTP routes change a
// source and after the official_libraries setting changes.
func (a *Application) ApplyLibraries(dropped ...string) error {
	if a.AddonFrontend() {
		// A frontend reads only the galleries of its flags and keeps no store.
		return nil
	}
	store, err := a.LibrarySources()
	if err != nil {
		return err
	}
	cfg := a.applicationConfig
	changed := store.SetOfficial(cfg.OfficialLibraries)
	prevM, prevB := cfg.Galleries, cfg.BackendGalleries
	cfg.Galleries, cfg.BackendGalleries = library.Reconcile(store, cfg.Galleries, cfg.BackendGalleries, dropped...)
	gallery.ResetGalleryModelCacheIfChanged(prevM, prevB, cfg)
	if changed || a.addonSources != nil {
		go func() {
			if err := store.RefreshEnabled(context.Background()); err != nil {
				xlog.Warn("could not refresh the library sources", "error", err)
			}
		}()
	}
	return nil
}

// applyOfficialLibrariesAtStartup drops the shipped default galleries from the
// lists the server reads when the persisted setting turned the official
// libraries off. The command line default is on, so this only acts on a saved
// setting.
func applyOfficialLibrariesAtStartup(cfg *config.ApplicationConfig) {
	if cfg.OfficialLibraries || cfg.DataPath == "" || cfg.Addons.IsFrontend() {
		return
	}
	store, err := catalog.OpenStore(catalog.StorePath(cfg.DataPath))
	if err != nil {
		xlog.Warn("official libraries are not turned off", "error", err)
		return
	}
	store.SetOfficial(false)
	cfg.Galleries, cfg.BackendGalleries = library.Reconcile(store, cfg.Galleries, cfg.BackendGalleries)
}

// AddonFrontend says whether this process is an add-on frontend. It keeps no
// owner state: nothing under the add-ons folder of the data path is opened.
func (a *Application) AddonFrontend() bool {
	return a != nil && a.applicationConfig != nil && a.applicationConfig.Addons.IsFrontend()
}
