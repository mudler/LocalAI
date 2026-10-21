// SPDX-License-Identifier: MIT
package library

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/xlog"
)

// MapGalleries makes each gallery a flag names a source of the user, of the
// given kind (model or backend), in gallery format. It is what keeps
// --galleries and --backend-galleries working next to the library: the same
// JSON still loads, and the entries also show in `local-ai library list`.
//
// It is additive and idempotent. A gallery whose kind and URL are already a
// source, whether a shipped default, one the user added with the CLI or one a
// former start mapped, is not added again. A shipped default the user removed
// stays removed.
func MapGalleries(store *catalog.Sources, kind catalog.Kind, galleries []config.Gallery) ([]catalog.Source, error) {
	var added []catalog.Source
	for _, g := range galleries {
		if g.URL == "" {
			continue
		}
		known, taken := false, map[string]bool{}
		for _, e := range store.Entries() {
			taken[e.ID] = true
			if e.Kind == kind && e.URL == g.URL {
				known = true
			}
		}
		if known {
			continue
		}
		id := freeID(taken, SourceID(g.Name, g.URL))
		name := g.Name
		if name == "" {
			name = id
		}
		src := catalog.Source{ID: id, Name: name, URL: g.URL, Enabled: true, Kind: kind, Format: catalog.FormatGallery}
		if err := store.AddUser(src); err != nil {
			return added, err
		}
		added = append(added, src)
	}
	return added, nil
}

// ApplyGalleries returns the galleries the server reads: the ones the flags
// named, minus those whose source is disabled or removed, plus the model
// libraries and the galleries the user added with `local-ai library add`.
// A gallery that is in the list already is not added a second time, so a
// deployment that only uses the flags reads exactly what it read before.
//
// A shipped default that the flags left out is not added back: a list that is
// set replaces the defaults, as it always did.
func ApplyGalleries(store *catalog.Sources, kind catalog.Kind, galleries []config.Gallery) []config.Gallery {
	state := map[string]catalog.Source{}
	var extra []catalog.Source
	for _, e := range store.Entries() {
		if e.Kind != kind {
			continue
		}
		state[e.URL] = e
	}
	out := make([]config.Gallery, 0, len(galleries))
	have := map[string]bool{}
	for _, g := range galleries {
		if e, ok := state[g.URL]; ok && (!e.Enabled || e.Removed) {
			continue
		}
		out = append(out, g)
		have[g.URL] = true
	}
	for _, e := range store.Entries() {
		if e.Kind == kind && e.Enabled && !e.Removed && !e.Default && !have[e.URL] && (e.Format == catalog.FormatGallery || kind == catalog.KindModel) {
			extra = append(extra, e)
		}
	}
	for _, e := range extra {
		out = append(out, config.Gallery{Name: e.Name, URL: e.URL, Library: e.Format != catalog.FormatGallery})
		have[e.URL] = true
	}
	return out
}

// Option returns the startup option that joins the galleries the flags named
// with the library sources. It must come after the options that set the
// galleries. If the store cannot be read or written the galleries stay as the
// flags set them and a warning says why: the library must never stop a
// deployment that worked before.
func Option(dataPath string, official, pilot bool) config.AppOption {
	return func(o *config.ApplicationConfig) {
		store, err := Open(dataPath, official, pilot)
		if err != nil {
			xlog.Warn("library sources are not applied", "error", err)
			return
		}
		if added, err := MapGalleries(store, catalog.KindModel, o.Galleries); err != nil {
			xlog.Warn("could not record the model galleries as library sources", "error", err)
		} else if len(added) > 0 {
			xlog.Info("model galleries added as library sources", "count", len(added))
		}
		if added, err := MapGalleries(store, catalog.KindBackend, o.BackendGalleries); err != nil {
			xlog.Warn("could not record the backend galleries as library sources", "error", err)
		} else if len(added) > 0 {
			xlog.Info("backend galleries added as library sources", "count", len(added))
		}
		o.Galleries = ApplyGalleries(store, catalog.KindModel, o.Galleries)
		o.BackendGalleries = ApplyGalleries(store, catalog.KindBackend, o.BackendGalleries)
	}
}

// parse is for tests: a JSON gallery list as a flag carries it.
func parse(list string) []config.Gallery {
	var g []config.Gallery
	_ = json.Unmarshal([]byte(list), &g)
	return g
}
