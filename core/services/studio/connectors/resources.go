// SPDX-License-Identifier: MIT
package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ResourceEntry is one external resource a binding lets its add-on reach. Key is
// the add-on's own name for it, such as a repository. ID is the host's name for
// the same resource, in the shape of a resource ID, so it can ride in a request.
type ResourceEntry struct {
	Key string `json:"key"`
	ID  string `json:"id"`
}

// ResourceID derives the host ID of a resource key. Keys compare without regard
// to case, so the ID is made from the lower-case key: "R-" plus ten hex
// characters of its hash, in lower case.
func ResourceID(key string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(key))))
	return "r-" + hex.EncodeToString(sum[:])[:10]
}

// NewResources makes the entries of a list of keys, in order. It trims the
// keys and drops repeats (compared without regard to case). It does not check
// them: Validate does.
func NewResources(keys []string) []ResourceEntry {
	var out []ResourceEntry
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[strings.ToLower(k)] {
			continue
		}
		seen[strings.ToLower(k)] = true
		out = append(out, ResourceEntry{Key: k, ID: ResourceID(k)})
	}
	return out
}

// ResourceKeys lists the keys of the entries.
func ResourceKeys(entries []ResourceEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	return out
}

func validateResources(entries []ResourceEntry) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	if len(entries) > MaxResources {
		return bad("a binding lists at most %d resources", MaxResources)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		n := utf8.RuneCountInString(e.Key)
		if n == 0 || n > MaxResourceKeyRunes || strings.TrimSpace(e.Key) != e.Key {
			return bad("a resource has 1 to %d characters, with no space at its ends", MaxResourceKeyRunes)
		}
		for _, r := range e.Key {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return bad("a resource has no space or control character")
			}
		}
		l := strings.ToLower(e.Key)
		if seen[l] {
			return bad("the resource %q is listed twice", e.Key)
		}
		seen[l] = true
		if e.ID != ResourceID(e.Key) {
			return bad("the ID of the resource %q does not match its key", e.Key)
		}
	}
	return nil
}

// ErrNoCredential means the person has no binding of the add-on.
var ErrNoCredential = errors.New("connectors: no binding of this add-on")

// NotAllowedError means no binding of the person lists the resource.
type NotAllowedError struct {
	// Allowed are the keys the person's bindings of the add-on list, sorted.
	Allowed []string
}

func (*NotAllowedError) Error() string { return "connectors: the resource is not on the list" }

// Pick chooses the binding and the entry for a call of an add-on's tool. The
// bindings are those of one person for one add-on, in the order the store
// lists them. A call that names a resource uses the first binding that lists
// it, without regard to case. A call that names none (key is empty) uses the
// first binding that holds a token and gets no entry. A person with no binding
// gets ErrNoCredential, and a key on no list gets a *NotAllowedError.
func Pick(bindings []Binding, key string) (Binding, ResourceEntry, error) {
	var usable []Binding
	for _, b := range bindings {
		if len(b.Token) > 0 {
			usable = append(usable, b)
		}
	}
	if len(usable) == 0 {
		return Binding{}, ResourceEntry{}, ErrNoCredential
	}
	if key == "" {
		return usable[0], ResourceEntry{}, nil
	}
	for _, b := range usable {
		for _, e := range b.Resources {
			if strings.EqualFold(e.Key, key) {
				return b, e, nil
			}
		}
	}
	var allowed []string
	for _, b := range usable {
		for _, e := range b.Resources {
			if !slices.Contains(allowed, e.Key) {
				allowed = append(allowed, e.Key)
			}
		}
	}
	slices.Sort(allowed)
	return Binding{}, ResourceEntry{}, &NotAllowedError{Allowed: allowed}
}
