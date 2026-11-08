package application

import (
	"context"
	"errors"
	"sort"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/backendupdates"
)

// refreshBackendLibraries asks the libraries that installed backends came from
// for their current index, so the next comparison sees what the library
// lists now. Only the add-on owner holds the index; a frontend asks the owner
// at each comparison, so it has nothing to refresh. A library that is turned
// off or removed is skipped, as the comparison skips it.
func (a *Application) refreshBackendLibraries(ctx context.Context) error {
	src := a.addonSources
	if src == nil {
		return nil
	}
	installed, err := gallery.ListSystemBackends(a.applicationConfig.SystemState)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, b := range installed {
		if b.IsSystem || b.Metadata == nil || b.Metadata.Library == "" || b.Metadata.Digest == "" {
			continue
		}
		seen[b.Metadata.Library] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		if _, err := src.Refresh(ctx, id); err != nil && !errors.Is(err, catalog.ErrUnavailable) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// loadedModels lists the models loaded on the cluster nodes with the backend
// each was loaded with. Without a node registry (standalone) there is nothing
// to list.
func (a *Application) loadedModels(ctx context.Context) ([]backendupdates.LoadedModel, error) {
	if a.distributed == nil || a.distributed.Registry == nil {
		return nil, nil
	}
	rows, err := a.distributed.Registry.ListAllLoadedModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]backendupdates.LoadedModel, 0, len(rows))
	for _, r := range rows {
		out = append(out, backendupdates.LoadedModel{NodeID: r.NodeID, Model: r.ModelName, Backend: r.BackendType})
	}
	return out, nil
}
