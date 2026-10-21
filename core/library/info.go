package library

import (
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

// Info is one record of the library as the HTTP routes and the UI show it.
// `local-ai library list` prints the same store records.
type Info struct {
	Kind   catalog.Kind `json:"kind"`
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	URL    string       `json:"url"`
	Format string       `json:"format,omitempty"`
	// Enabled is what the host does with the source: false for a source that
	// is turned off, removed, or a shipped default while the official
	// libraries are off. Suspended tells the last case apart.
	Enabled   bool `json:"enabled"`
	Default   bool `json:"default"`
	Removed   bool `json:"removed"`
	Suspended bool `json:"suspended"`
	// Conflict explains how this source clashes with another one, when it does.
	Conflict      string                      `json:"conflict,omitempty"`
	AuthReference string                      `json:"authReference,omitempty"`
	Policy        *catalog.VerificationPolicy `json:"policy,omitempty"`
	Refresh       *catalog.RefreshState       `json:"refresh,omitempty"`
}

// List returns every record of the store, tombstones included, by kind and id.
func List(store *catalog.Sources) []Info {
	conflicts := store.Conflicts()
	entries := store.Entries()
	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		in := Info{
			Kind: e.Kind, ID: e.ID, Name: e.Name, URL: e.URL, Format: e.Format,
			Enabled: e.Enabled && !e.Removed && !e.Suspended,
			Default: e.Default, Removed: e.Removed, Suspended: e.Suspended && !e.Removed,
			Conflict: conflicts[e.ID], AuthReference: e.AuthReference,
		}
		p := e.Policy
		in.Policy = &p
		if st, ok := store.State(e.ID); ok {
			in.Refresh = &st
		}
		out = append(out, in)
	}
	return out
}

// Remove removes a source and says whether it was a shipped default, which
// leaves a tombstone instead of deleting the record. It is what both
// `local-ai library remove` and the HTTP route call.
func Remove(store *catalog.Sources, id string) (wasDefault bool, err error) {
	for _, e := range store.Entries() {
		if e.ID == id {
			wasDefault = e.Default
		}
	}
	if err = store.RemoveSource(id); err != nil {
		return false, err
	}
	return wasDefault, nil
}

// URLOf returns the address of the source with this id, or "" when there is none.
func URLOf(store *catalog.Sources, id string) string {
	for _, e := range store.Entries() {
		if e.ID == id {
			return e.URL
		}
	}
	return ""
}

// IsNotFound reports whether err says no source has that id.
func IsNotFound(err error) bool { return errors.Is(err, catalog.ErrNotFound) }
