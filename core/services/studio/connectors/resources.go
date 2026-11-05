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

	"github.com/mudler/LocalAI/pkg/addons/contract"
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

// AnyResource is the wildcard entry that lists every resource the credential
// reaches. A kind must declare wildcardResources for the host to honor it.
const AnyResource = contract.AnyResource

// ownerWildcard returns the owner of an entry written owner/*: one owner, a
// slash and a star, with no other star or slash.
func ownerWildcard(entry string) (owner string, ok bool) {
	owner, rest, found := strings.Cut(entry, "/")
	if !found || rest != "*" || owner == "" || strings.ContainsAny(owner, "*/") {
		return "", false
	}
	return owner, true
}

// IsWildcard reports an entry that stands for many resources: * or owner/*.
func IsWildcard(entry string) bool {
	_, ok := ownerWildcard(entry)
	return ok || entry == AnyResource
}

// HasAnyResource reports a list that holds the entry *.
func HasAnyResource(entries []ResourceEntry) bool {
	return slices.ContainsFunc(entries, func(e ResourceEntry) bool { return e.Key == AnyResource })
}

// MatchResource reports whether key is covered by the entry. A plain entry
// matches the same name without regard to case, as GitHub names do. owner/*
// matches owner/name for the exact owner (so not other-owner/name, and not
// owner-evil/name) and one name, never a deeper path. * matches any one key.
// A key that holds a star is never covered by a wildcard: it is not a name.
// Only the wildcard kinds of a binding call this; see Entry.
func MatchResource(entry, key string) bool {
	if key == "" {
		return false
	}
	if strings.Contains(key, "*") {
		return key == AnyResource && entry == AnyResource
	}
	if strings.EqualFold(entry, key) {
		return true
	}
	if entry == AnyResource {
		return true
	}
	owner, ok := ownerWildcard(entry)
	if !ok {
		return false
	}
	k, name, found := strings.Cut(key, "/")
	return found && name != "" && !strings.Contains(name, "/") && strings.EqualFold(k, owner)
}

// ValidateWildcards checks the wildcards of a list for a kind. A kind without
// wildcardResources takes every entry as an exact name, so nothing is
// refused here. With it, a star may only stand alone (*) or after an owner
// (owner/*), and * excludes every other entry because it already covers them.
func ValidateWildcards(entries []ResourceEntry, wildcards bool) error {
	if !wildcards {
		return nil
	}
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	for _, e := range entries {
		if strings.Contains(e.Key, "*") && !IsWildcard(e.Key) {
			return bad("%q: a star may only stand alone (*) or after an owner (owner/*)", e.Key)
		}
	}
	if HasAnyResource(entries) && len(entries) > 1 {
		return bad("the entry * covers every resource: remove the other entries")
	}
	return nil
}

// Entry returns the entry of the binding that the call names. It is the only
// resource check: the host uses the one binding the person selected and holds
// the call to its list. A call that names no resource (key is empty) gets no
// entry. A key on the list is matched without regard to case. Any other key
// gets a *NotAllowedError that lists the keys of this binding, sorted. A
// binding without a token is no credential: ErrNoCredential.
func Entry(b Binding, key string) (ResourceEntry, error) { return EntryFor(b, key, false) }

// EntryFor is Entry for a kind that may declare wildcardResources. With
// wildcards the list may hold owner/* and *, and a key they cover gets an
// entry made for that key: the key as sent and its own host ID, so the
// add-on and the audit line see the real resource and never the wildcard.
// The key * alone names no resource but the whole reach of the credential;
// only the entry * covers it, for the tools that enumerate (see
// contract.ToolDefinition.Enumerates).
func EntryFor(b Binding, key string, wildcards bool) (ResourceEntry, error) {
	if len(b.Token) == 0 {
		return ResourceEntry{}, ErrNoCredential
	}
	if key == "" {
		return ResourceEntry{}, nil
	}
	for _, e := range b.Resources {
		// Without wildcards this is the old exact match. With them, a key that
		// holds a star is the wildcard written out, not a name: only * itself
		// may be asked for.
		if strings.EqualFold(e.Key, key) && (!wildcards || key == AnyResource || !strings.Contains(key, "*")) {
			return e, nil
		}
		if wildcards && IsWildcard(e.Key) && MatchResource(e.Key, key) {
			return ResourceEntry{Key: key, ID: ResourceID(key)}, nil
		}
	}
	allowed := ResourceKeys(b.Resources)
	slices.Sort(allowed)
	return ResourceEntry{}, &NotAllowedError{Allowed: allowed}
}
