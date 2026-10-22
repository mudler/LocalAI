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
		if e, ok := state[g.URL]; ok && (!e.Enabled || e.Removed || e.Suspended) {
			continue
		}
		out = append(out, g)
		have[g.URL] = true
	}
	for _, e := range store.Entries() {
		if e.Kind == kind && e.Enabled && !e.Removed && !e.Suspended && (!e.Default || e.Format != catalog.FormatGallery) && readsAsGallery(kind, e) && !have[e.URL] {
			extra = append(extra, e)
		}
	}
	for _, e := range extra {
		out = append(out, config.Gallery{Name: e.Name, URL: e.URL, Library: e.Format != catalog.FormatGallery})
		have[e.URL] = true
	}
	return out
}

// readsAsGallery says whether the server hands a source to the gallery reader.
// A model source always is: a model library is read there. A backend source is
// when it is in gallery format, or when it is the shipped backend library,
// which is the backend source of a build that reads the library. A backend
// library the user added is read by the add-on catalog only, which installs
// its images by digest and holds them to the signature policy of the library.
func readsAsGallery(kind catalog.Kind, e catalog.Source) bool {
	return kind == catalog.KindModel || e.Format == catalog.FormatGallery || (kind == catalog.KindBackend && e.Default)
}

// Option returns the startup option that joins the galleries the flags named
// with the library sources. It must come after the options that set the
// galleries. If the store cannot be read or written the galleries stay as the
// flags set them and a warning says why: the library must never stop a
// deployment that worked before.
func Option(dataPath string, official, backendLibrary, useOfficial bool) config.AppOption {
	return func(o *config.ApplicationConfig) {
		store, err := Open(dataPath, official, backendLibrary)
		if err != nil {
			xlog.Warn("library sources are not applied", "error", err)
			return
		}
		store.SetOfficial(useOfficial)
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

// Reconcile makes the galleries the server reads match the store, for a store
// that changed while the server runs: a source turned on or off, added,
// removed, or the official-libraries switch. A gallery that is already listed
// keeps its mirrors and order; one the store no longer enables is dropped;
// an enabled source that is not listed yet is appended. A gallery that is
// not a source at all is kept. It returns the new
// model and backend galleries.
//
// dropped names the addresses of sources that were deleted: a deleted source is
// no longer in the store, so only the caller can say its gallery must go.
func Reconcile(store *catalog.Sources, models, backends []config.Gallery, dropped ...string) ([]config.Gallery, []config.Gallery) {
	return reconcileKind(store, catalog.KindModel, models, dropped), reconcileKind(store, catalog.KindBackend, backends, dropped)
}

func reconcileKind(store *catalog.Sources, kind catalog.Kind, current []config.Gallery, dropped []string) []config.Gallery {
	live := map[string]catalog.Source{}
	known := map[string]bool{}
	for _, u := range dropped {
		known[u] = true
	}
	for _, e := range store.Entries() {
		if e.Kind == kind {
			known[e.URL] = true
		}
		if e.Kind == kind && e.Enabled && !e.Removed && !e.Suspended && readsAsGallery(kind, e) {
			live[e.URL] = e
		}
	}
	out := make([]config.Gallery, 0, len(current))
	have := map[string]bool{}
	for _, g := range current {
		// A gallery the store does not know is not the store's to drop.
		if _, ok := live[g.URL]; ok || !known[g.URL] {
			out = append(out, g)
			have[g.URL] = true
		}
	}
	for _, e := range store.Entries() {
		if _, ok := live[e.URL]; ok && e.Kind == kind && readsAsGallery(kind, e) && !have[e.URL] {
			out = append(out, config.Gallery{Name: e.Name, URL: e.URL, Library: e.Format != catalog.FormatGallery})
			have[e.URL] = true
		}
	}
	return out
}

// parse is for tests: a JSON gallery list as a flag carries it.
func parse(list string) []config.Gallery {
	var g []config.Gallery
	_ = json.Unmarshal([]byte(list), &g)
	return g
}
