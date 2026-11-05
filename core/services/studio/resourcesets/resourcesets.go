// Package resourcesets holds the type that names, for each add-on whose
// resources are scoped per agent, the resources a saved agent may use and the
// most it may do with each. It is a leaf package so that the stores, the API
// schema and the service share one JSON form.
package resourcesets

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The modes of an entry. Read lets the agent use the tools that only read.
// Write lets it use every tool that takes the resource.
const (
	ModeRead  = "read"
	ModeWrite = "write"
)

// Limits of a set.
const (
	MaxAddons          = 100
	MaxEntriesPerAddon = 100
	MaxIDRunes         = 64
)

// Entry is one resource of an agent's list.
type Entry struct {
	// ID is the resource ID the add-on host uses.
	ID string `json:"id" example:"db-1a2b3c"`
	// Mode is read or write.
	Mode string `json:"mode" enums:"read,write" example:"read"`
}

// Sets maps an add-on ID to the resources the agent may use. An add-on with no
// entry, or an empty list, gives the agent none; the field has no effect on an
// add-on that does not opt in to per-agent resources.
type Sets map[string][]Entry

var (
	addonIDShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	// idShape is the shape of a resource ID of the add-on host (see
	// toolargs.ValidResourceID): a plain token, so an ID is never a path.
	idShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

// Valid reports whether mode is read or write.
func Valid(mode string) bool { return mode == ModeRead || mode == ModeWrite }

// Normalize drops the entries of an empty list and the repeats of an ID. When
// an ID appears twice the first entry stays, with the higher of the two modes
// (a repeat never narrows what the first said).
func Normalize(s Sets) Sets {
	var out Sets
	for addon, entries := range s {
		var kept []Entry
		for _, e := range entries {
			i := slices.IndexFunc(kept, func(k Entry) bool { return k.ID == e.ID })
			switch {
			case i < 0:
				kept = append(kept, e)
			case e.Mode == ModeWrite:
				kept[i].Mode = ModeWrite
			}
		}
		if len(kept) == 0 {
			continue
		}
		if out == nil {
			out = Sets{}
		}
		out[addon] = kept
	}
	return out
}

// Validate checks the limits and the shape of a set. The text of the error is
// plain and names no field of the program.
func Validate(s Sets) error {
	if len(s) > MaxAddons {
		return fmt.Errorf("resources has more than %d add-ons", MaxAddons)
	}
	for addon, entries := range s {
		if !addonIDShape.MatchString(addon) {
			return fmt.Errorf("resources needs an add-on ID")
		}
		if len(entries) > MaxEntriesPerAddon {
			return fmt.Errorf("resources of %s has more than %d entries", addon, MaxEntriesPerAddon)
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if strings.TrimSpace(e.ID) == "" || utf8.RuneCountInString(e.ID) > MaxIDRunes || !idShape.MatchString(e.ID) {
				return fmt.Errorf("a resource of %s needs an ID", addon)
			}
			if !Valid(e.Mode) {
				return fmt.Errorf("the mode of a resource must be read or write")
			}
			if seen[e.ID] {
				return fmt.Errorf("a resource of %s is listed twice", addon)
			}
			seen[e.ID] = true
		}
	}
	return nil
}

// Clone copies s so a caller cannot change a stored value. An empty set is nil.
func Clone(s Sets) Sets {
	if len(s) == 0 {
		return nil
	}
	out := make(Sets, len(s))
	for k, v := range s {
		out[k] = slices.Clone(v)
	}
	return out
}

// Equal reports whether two sets list the same resources in the same order.
func Equal(a, b Sets) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// ModeOf returns the mode the set gives for one resource of one add-on, and
// whether the resource is listed at all.
func (s Sets) ModeOf(addon, id string) (string, bool) {
	for _, e := range s[addon] {
		if e.ID == id {
			return e.Mode, true
		}
	}
	return "", false
}

// Allows says whether a call may use the resource: any listed resource for a
// call that reads, and only a write entry for a call that changes something.
func (s Sets) Allows(addon, id string, write bool) bool {
	mode, ok := s.ModeOf(addon, id)
	if !ok {
		return false
	}
	return !write || mode == ModeWrite
}
