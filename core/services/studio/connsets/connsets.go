// Package connsets holds the type that names, for each add-on whose tools run
// with a credential, the connections (connector binding IDs) a chat or an agent
// may use. It is a leaf package so that the stores, the API schema and the
// service share one JSON form.
package connsets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Sets maps an add-on ID to the binding IDs enabled for it, in the order the
// person gave them. An add-on with no entry has no explicit choice.
type Sets map[string][]string

// UnmarshalJSON reads the current form (add-on ID to a list of binding IDs)
// and the form that stored one binding per add-on (add-on ID to a string), so
// data written before a set was possible keeps working. An empty string is no
// choice, as it was.
func (s *Sets) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*s = nil
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	out := make(Sets, len(m))
	for addon, v := range m {
		v = bytes.TrimSpace(v)
		switch {
		case len(v) > 0 && v[0] == '"':
			var one string
			if err := json.Unmarshal(v, &one); err != nil {
				return err
			}
			if one != "" {
				out[addon] = []string{one}
			}
		case len(v) > 0 && v[0] == '[':
			var list []string
			if err := json.Unmarshal(v, &list); err != nil {
				return err
			}
			if list = Dedup(list); len(list) > 0 {
				out[addon] = list
			}
		case bytes.Equal(v, []byte("null")):
		default:
			return fmt.Errorf("connections for %q: want a list of binding IDs", addon)
		}
	}
	*s = out
	return nil
}

// Dedup drops empty entries and repeats, keeping the first of each.
func Dedup(ids []string) []string {
	var out []string
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
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

// Equal reports whether two sets name the same bindings in the same order.
func Equal(a, b Sets) bool { return maps.EqualFunc(a, b, slices.Equal[[]string]) }

// Single builds a set that names one binding for one add-on.
func Single(addon, id string) Sets { return Sets{addon: {id}} }
