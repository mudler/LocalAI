package modelsearch

import (
	"context"
	"slices"

	"github.com/mudler/xlog"
)

// Host ties the Searcher to the add-on sources and to the enabled set of a
// person. The REST route and the assistant tool both go through it, so an
// add-on source reaches both with no extra work.
type Host struct {
	Searcher *Searcher
	// Sync registers the add-on sources. Nil: only the providers already in
	// the Searcher exist.
	Sync *AddonSync
	// Selection holds the sources a person turned off. Nil: every source is on.
	Selection *Selection
}

// NewHost returns a Host. backend and kv may be nil.
func NewHost(s *Searcher, backend AddonBackend, kv SelectionKV) *Host {
	h := &Host{Searcher: s}
	if backend != nil {
		h.Sync = NewAddonSync(s, backend)
	}
	if kv != nil {
		h.Selection = NewSelection(kv)
	}
	return h
}

// Overview is the list of sources of one person.
type Overview struct {
	Sources []Source `json:"sources"`
	// Enabled is the enabled set: every source the person can see that they
	// did not turn off, in the order of Sources.
	Enabled []string `json:"enabled"`
	// Missing is kept for older clients and is always empty: the store holds
	// what the person turned off, so no stored ID asks for an add-on to be
	// installed.
	Missing []string `json:"missing"`
}

// Visible syncs the add-on sources and returns the IDs of the sources the
// person can use: the built-in ones and the add-ons they can see. A broker that
// cannot answer leaves the built-in ones.
func (h *Host) Visible(ctx context.Context, user string) []string {
	ctx = WithUser(ctx, user)
	if h.Sync == nil {
		return h.Searcher.Names()
	}
	ids, err := h.Sync.SyncVisible(ctx, user)
	if err != nil {
		xlog.Warn("model search: add-on sources unavailable, only the built-in ones are searched", "error", err)
	}
	// The built-in sources, then the add-on sources of this person. A source
	// that another person can see is registered but not in this list.
	var out []string
	for _, n := range h.Searcher.Names() {
		if !h.Sync.owns(n) || slices.Contains(ids, n) {
			out = append(out, n)
		}
	}
	return out
}

// Enabled returns the enabled set of the person: every source they can see
// that they did not turn off. A store that cannot answer gives every visible
// source, and a failure does not stop the search.
func (h *Host) Enabled(ctx context.Context, user string) []string {
	return h.enabledOf(ctx, user, h.Visible(ctx, user))
}

func (h *Host) enabledOf(ctx context.Context, user string, visible []string) []string {
	ids, err := h.Selection.Enabled(ctx, user, visible)
	if err != nil {
		xlog.Warn("model search: the choice of sources could not be read, every source is on", "error", err)
		return slices.Clone(visible)
	}
	return ids
}

// Active returns the sources to search for the person: the enabled set. The
// order is the order of the visible sources.
func (h *Host) Active(ctx context.Context, user string) []string {
	return h.Enabled(ctx, user)
}

// Overview lists the sources the person can see and their choice.
func (h *Host) Overview(ctx context.Context, user string) Overview {
	visible := h.Visible(ctx, user)
	out := Overview{Sources: []Source{}, Enabled: h.enabledOf(ctx, user, visible), Missing: []string{}}
	for _, id := range visible {
		if src, ok := h.Searcher.Source(id); ok {
			out.Sources = append(out.Sources, src)
		}
	}
	return out
}

// SetEnabled stores the enabled set, as the list of what is off.
func (h *Host) SetEnabled(ctx context.Context, user string, ids []string) error {
	return h.Selection.Set(ctx, user, ids, h.Visible(ctx, user))
}

// Search searches the sources of the person: names when given, else the
// enabled set. An empty set answers with no result and no status.
func (h *Host) Search(ctx context.Context, user string, q Query, names []string) Response {
	ctx = WithUser(ctx, user)
	if len(names) == 0 {
		names = h.Active(ctx, user)
		if len(names) == 0 {
			return Response{Results: []Result{}, Providers: []ProviderStatus{}}
		}
	}
	return h.Searcher.Search(ctx, q, names)
}
