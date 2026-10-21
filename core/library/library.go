// SPDX-License-Identifier: MIT

// Package library is the one list of library sources: the places LocalAI reads
// add-ons, backends and models from. The list lives in the source store of the
// add-on owner (see catalog.StorePath), so the server, the startup flags and
// `local-ai library` all work on the same file, with or without a running
// server.
package library

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

const (
	// DefaultModelSourceID and DefaultBackendSourceID name the shipped model
	// and backend gallery indexes as sources.
	DefaultModelSourceID   = "localai"
	DefaultBackendSourceID = "localai-backends"
)

// Defaults returns the sources LocalAI ships: the official add-on library, the
// pilot backend library, the model library, the model gallery index and the
// backend gallery index. The add-on library is left out when official is false
// and the backend library when pilot is false. A default can be removed; the
// removal is remembered.
func Defaults(official, pilot bool) []catalog.Source {
	var out []catalog.Source
	if official {
		out = append(out, catalog.OfficialLibrary())
	}
	if pilot {
		out = append(out, catalog.OfficialBackendLibrary())
	}
	out = append(out,
		catalog.OfficialModelLibrary(),
		defaultGallery(catalog.KindModel, DefaultModelSourceID, "Official models", config.DefaultGalleriesJSON),
		defaultGallery(catalog.KindBackend, DefaultBackendSourceID, "Official backends", config.DefaultBackendGalleriesJSON),
	)
	return out
}

// defaultGallery turns the first gallery of a shipped JSON list into a source.
// The lists are compile-time constants, so a parse error is a bug.
func defaultGallery(kind catalog.Kind, id, name, shipped string) catalog.Source {
	var g []config.Gallery
	if err := json.Unmarshal([]byte(shipped), &g); err != nil || len(g) == 0 {
		panic("invalid built-in gallery JSON for " + id)
	}
	return catalog.Source{ID: id, Name: name, URL: g[0].URL, Enabled: true, Kind: kind, Format: catalog.FormatGallery}
}

// Open opens the store under dataPath and adds the shipped defaults that have
// no record yet.
func Open(dataPath string, official, pilot bool) (*catalog.Sources, error) {
	store, err := catalog.OpenStore(catalog.StorePath(dataPath))
	if err != nil {
		return nil, err
	}
	if _, err = store.SeedDefaults(Defaults(official, pilot)...); err != nil {
		return nil, fmt.Errorf("library defaults: %w", err)
	}
	return store, nil
}

var idChars = regexp.MustCompile(`[^a-z0-9]+`)

// SourceID derives a source id from a name or, failing that, a URL. The id is
// lowercase letters, digits and dashes, and starts with a letter.
func SourceID(name, url string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = url
		for _, p := range []string{"https://", "http://", "oci://", "file://", "github:"} {
			base = strings.TrimPrefix(base, p)
		}
	}
	id := strings.Trim(idChars.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if id == "" || id[0] < 'a' || id[0] > 'z' {
		id = "src-" + id
	}
	id = strings.TrimRight(id, "-")
	if len(id) > 48 {
		id = strings.TrimRight(id[:48], "-")
	}
	return id
}

// freeID returns id, or id with a number when another record has it.
func freeID(taken map[string]bool, id string) string {
	if !taken[id] {
		return id
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s-%d", id, n); !taken[c] {
			return c
		}
	}
}

// Add adds a user source. When id is empty one is derived from the name or
// URL. A source with the same kind and URL is refused, so the same index is
// not listed twice.
func Add(store *catalog.Sources, kind catalog.Kind, url, id, name, format string) (catalog.Source, error) {
	for _, e := range store.Entries() {
		if e.Kind == kind && e.URL == url && !e.IsTombstone() {
			return catalog.Source{}, fmt.Errorf("%s is already a %s source (%s)", url, kind, e.ID)
		}
	}
	if id == "" {
		taken := map[string]bool{}
		for _, e := range store.Entries() {
			taken[e.ID] = true
		}
		id = freeID(taken, SourceID(name, url))
	}
	if name == "" {
		name = id
	}
	src := catalog.Source{ID: id, Name: name, URL: url, Enabled: true, Kind: kind, Format: format}
	if err := store.AddUser(src); err != nil {
		return catalog.Source{}, err
	}
	return src, nil
}
