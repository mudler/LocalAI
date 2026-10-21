// SPDX-License-Identifier: MIT
package catalog

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrConflict is returned when two sources of the user list the same entry.
	ErrConflict = errors.New("two library sources list the same entry")
	// ErrShadowed is returned when a shipped default is asked for an entry that
	// a source of the user provides: the default is ignored for that entry.
	ErrShadowed = errors.New("the entry is provided by a library source of your own")
)

// ConflictError names the entry and the two sources that both list it. The
// host never picks one of them silently.
type ConflictError struct {
	Kind   Kind
	ID     string
	First  string
	Second string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("the %s %q is listed by two of your library sources, %q and %q; disable or remove one of them (local-ai library disable %s)",
		e.Kind, e.ID, e.First, e.Second, e.Second)
}

func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

type entryKey struct {
	kind Kind
	id   string
}

// owners are the enabled sources that list one entry: those the user added and
// those the host ships. Each list is sorted by source id.
type owners struct {
	user, shipped []string
}

// entryOwners maps every entry the enabled sources list to the sources that
// list it. The caller holds s.mu. A source whose last fetch failed or never
// ran lists nothing.
//
// The rule it supports is: a source of the user wins over a shipped default;
// two sources of the user that list the same entry are a conflict; among the
// shipped defaults nothing changes, and each lists its own entries.
func (s *Sources) entryOwners() map[entryKey]*owners {
	out := map[entryKey]*owners{}
	for id, src := range s.sources {
		c, ok := s.cache[id]
		if !src.readByCatalog() || !ok || c.failed {
			continue
		}
		seen := map[entryKey]bool{}
		for _, r := range c.releases {
			k := entryKey{r.EntryKind(), r.AddonID}
			if seen[k] {
				continue
			}
			seen[k] = true
			o := out[k]
			if o == nil {
				o = &owners{}
				out[k] = o
			}
			if src.Default {
				o.shipped = append(o.shipped, id)
			} else {
				o.user = append(o.user, id)
			}
		}
	}
	for _, o := range out {
		sort.Strings(o.user)
		sort.Strings(o.shipped)
	}
	return out
}

// check applies the rule to one entry as seen from the source that lists it.
// It returns a ConflictError when two sources of the user list the entry, and
// ErrShadowed when sourceID is a shipped default and a source of the user lists
// the entry too.
func (o *owners) check(k entryKey, sourceIsDefault bool) error {
	if o == nil {
		return nil
	}
	if len(o.user) >= 2 {
		return &ConflictError{Kind: k.kind, ID: k.id, First: o.user[0], Second: o.user[1]}
	}
	if len(o.user) == 1 && sourceIsDefault {
		return ErrShadowed
	}
	return nil
}
