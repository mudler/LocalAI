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

// ErrNoCredential means the selected binding is not the person's, is not of
// the add-on, or holds no token.
var ErrNoCredential = errors.New("connectors: no binding of this add-on")

// NotAllowedError means the selected binding does not list the resource.
type NotAllowedError struct {
	// Allowed are the keys the selected binding lists, sorted.
	Allowed []string
}

func (*NotAllowedError) Error() string { return "connectors: the resource is not on the list" }

// ErrNoConnection means a tool call has no selected connection: the person
// has bindings of the add-on (or none is chosen yet) and the chat or the agent
// names none. The host never picks one for them.
var ErrNoConnection = errors.New("connectors: no connection is selected")

// Entry returns the entry of the binding that the call names. It is the only
// resource check: the host uses the one binding the person selected and holds
// the call to its list. A call that names no resource (key is empty) gets no
// entry. A key on the list is matched without regard to case. Any other key
// gets a *NotAllowedError that lists the keys of this binding, sorted. A
// binding without a token is no credential: ErrNoCredential.
func Entry(b Binding, key string) (ResourceEntry, error) {
	if len(b.Token) == 0 {
		return ResourceEntry{}, ErrNoCredential
	}
	if key == "" {
		return ResourceEntry{}, nil
	}
	for _, e := range b.Resources {
		if strings.EqualFold(e.Key, key) {
			return e, nil
		}
	}
	allowed := ResourceKeys(b.Resources)
	slices.Sort(allowed)
	return ResourceEntry{}, &NotAllowedError{Allowed: allowed}
}
