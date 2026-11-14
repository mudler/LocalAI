package modelsearch

import (
	"context"
	"fmt"
	"sync"

	"github.com/mudler/xlog"
)

// AddonSync keeps the add-on sources of a Searcher in step with the add-ons
// that are installed. Sync runs at the start of a search, so an add-on that
// is installed or removed shows up with no restart and no hook into the
// add-on lifecycle.
//
// An add-on that is installed and stopped stays registered: a search says
// that source failed, and the others return. An add-on that is removed is
// unregistered.
type AddonSync struct {
	s *Searcher
	b AddonBackend

	mu sync.Mutex
	// owned maps a source ID that this sync registered to its add-on.
	owned map[string]string
	// seen holds, for each person, the source IDs the broker showed them at
	// their last sync. The Searcher is shared by every person, so a source stays
	// registered while any person can see it; what one person may search is a
	// filter of that person's request (their own list), never a change to the
	// registry.
	seen map[string]map[string]bool
}

// NewAddonSync returns a sync for the searcher and the broker.
func NewAddonSync(s *Searcher, b AddonBackend) *AddonSync {
	return &AddonSync{s: s, b: b, owned: map[string]string{}, seen: map[string]map[string]bool{}}
}

// Sync registers the sources that the person can see. See SyncVisible.
func (a *AddonSync) Sync(ctx context.Context, user string) error {
	_, err := a.SyncVisible(ctx, user)
	return err
}

// SyncVisible registers the add-on sources that the person can see and returns
// their IDs, in broker order. A source that this person cannot see stays
// registered when another person can: it is unregistered only when no person
// who synced sees it any more. A memory of another person is checked with the
// broker again before it keeps a source alive, so an idle person's old answer
// never holds a removed add-on in the registry. A source ID that is already taken (by a
// built-in source or by another add-on) is refused and audited; the first one
// wins.
func (a *AddonSync) SyncVisible(ctx context.Context, user string) ([]string, error) {
	list, err := a.b.Sources(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("list the add-on sources: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	mine := map[string]bool{}
	var visible []string
	for _, src := range list {
		id := src.Search.Source.ID
		if owner, ok := a.owned[id]; ok && owner != src.AddonID {
			a.refuse(id, src.AddonID, owner)
			continue
		}
		if _, ok := a.owned[id]; !ok && a.s.Has(id) {
			a.refuse(id, src.AddonID, "the host")
			continue
		}
		mine[id] = true
		visible = append(visible, id)
		a.owned[id] = src.AddonID
		a.s.Register(newAddonProvider(a.b, src))
	}
	a.seen[user] = mine
	// Another person's memory of a source may be old: they may be idle while
	// the add-on is removed. Before a source this person cannot see is kept for
	// someone else, ask the broker again for that person.
	for id := range a.owned {
		if mine[id] {
			continue
		}
		for other, set := range a.seen {
			if other == user || !set[id] {
				continue
			}
			again, err := a.b.Sources(ctx, other)
			if err != nil {
				continue // cannot tell: keep what is known
			}
			fresh := map[string]bool{}
			for _, src := range again {
				if a.owned[src.Search.Source.ID] == src.AddonID {
					fresh[src.Search.Source.ID] = true
				}
			}
			a.seen[other] = fresh
		}
	}
	for id := range a.owned {
		if !a.anySees(id) {
			a.s.Unregister(id)
			delete(a.owned, id)
		}
	}
	return visible, nil
}

func (a *AddonSync) anySees(id string) bool {
	for _, set := range a.seen {
		if set[id] {
			return true
		}
	}
	return false
}

func (a *AddonSync) refuse(id, addon, holder string) {
	// The audit line names the add-ons and never any text of a result.
	xlog.Warn("model search: a second add-on declares a source ID that is already in use, refused",
		"audit", true, "source", id, "addon", addon, "held_by", holder)
}

// owns reports whether the sync registered the source.
func (a *AddonSync) owns(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.owned[id]
	return ok
}
