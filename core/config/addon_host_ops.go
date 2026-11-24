// SPDX-License-Identifier: MIT
package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	addonIDForHostOps = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hostOpPrefix      = regexp.MustCompile(`^[a-z]+\.([a-z]+\.)*$`)
	hostOpExact       = regexp.MustCompile(`^[a-z]+(\.[a-z]+)+$`)
)

// ParseAddonHostOps reads a comma list of addon-id=op-prefix pairs. An add-on
// may appear more than once. An empty string names nobody.
func ParseAddonHostOps(s string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, prefix, ok := strings.Cut(item, "=")
		id, prefix = strings.TrimSpace(id), strings.TrimSpace(prefix)
		if !ok || !addonIDForHostOps.MatchString(id) || !hostOpPrefix.MatchString(prefix) {
			return nil, fmt.Errorf("invalid host operation entry %q: want addon-id=op-prefix, such as custom-addon=workspace.", item)
		}
		out[id] = append(out[id], prefix)
	}
	return out, nil
}

// WithAddonHostOps sets which add-ons may call which host operations.
// Nil and empty maps grant nothing. The run command resolves explicit host
// policy before passing its startup snapshot here.
func WithAddonHostOps(ops map[string][]string) AppOption {
	snapshot := CloneAddonHostOps(ops)
	return func(o *ApplicationConfig) { o.AddonHostOps = CloneAddonHostOps(snapshot) }
}

// validDenyEntry accepts what a deny rule may name: a prefix (workspace.),
// one exact operation (workspace.admin) or the single token "*".
func validDenyEntry(s string) bool {
	return s == "*" || hostOpPrefix.MatchString(s) || hostOpExact.MatchString(s)
}

// ParseAddonHostDeny reads a comma list of addon-id=entry pairs, where addon-id
// may be "*" for every add-on and entry is a prefix, an exact operation name
// or "*". Duplicates are errors; "*" must be the only entry of its add-on.
func ParseAddonHostDeny(s string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, entry, ok := strings.Cut(item, "=")
		id, entry = strings.TrimSpace(id), strings.TrimSpace(entry)
		if !ok || !(id == "*" || addonIDForHostOps.MatchString(id)) || !validDenyEntry(entry) {
			return nil, fmt.Errorf("invalid host deny entry %q: want addon-id=op-prefix or *=op-prefix, such as *=egress. or custom-addon=*", item)
		}
		for _, existing := range out[id] {
			if existing == entry {
				return nil, fmt.Errorf("duplicate host deny entry %q", item)
			}
		}
		out[id] = append(out[id], entry)
	}
	if len(out) > 128 {
		return nil, fmt.Errorf("host deny exceeds 128 addons")
	}
	for id, entries := range out {
		if len(entries) > 32 {
			return nil, fmt.Errorf("host deny exceeds 32 entries for %q", id)
		}
		for _, e := range entries {
			if e == "*" && len(entries) != 1 {
				return nil, fmt.Errorf(`host deny "*" must be the only entry of %q`, id)
			}
		}
	}
	return out, nil
}

// MergeAddonHostDeny unions deny maps. Entries stay in first-seen order and a
// "*" absorbs every other entry of its key. The result is never nil.
func MergeAddonHostDeny(maps ...map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, m := range maps {
		for id, entries := range m {
			for _, e := range entries {
				if !slices.Contains(out[id], e) {
					out[id] = append(out[id], e)
				}
			}
		}
	}
	for id, entries := range out {
		if slices.Contains(entries, "*") {
			out[id] = []string{"*"}
		}
	}
	return out
}

// ParseAddonHostBlanket validates the deployment blanket mode. The empty
// string means the deployment does not set it and the UI decides.
func ParseAddonHostBlanket(s string) (string, error) {
	switch s = strings.TrimSpace(s); s {
	case "", "off", "official", "any":
		return s, nil
	}
	return "", fmt.Errorf("invalid --addon-host-blanket %q: want off, official or any", s)
}

// WithAddonHostDeny sets the startup deny rules. The map is copied.
func WithAddonHostDeny(deny map[string][]string) AppOption {
	snapshot := CloneAddonHostOps(deny)
	return func(o *ApplicationConfig) { o.AddonHostDeny = CloneAddonHostOps(snapshot) }
}

// WithAddonHostOpsSource records where the startup grants came from, for the
// startup log. A path after a colon is dropped.
func WithAddonHostOpsSource(source string) AppOption {
	kind, _, _ := strings.Cut(source, ":")
	return func(o *ApplicationConfig) { o.AddonHostOpsSource = kind }
}

// WithAddonHostBlanket sets the deployment blanket mode ("" leaves it to the UI).
func WithAddonHostBlanket(mode string) AppOption {
	return func(o *ApplicationConfig) { o.AddonHostBlanket = mode }
}

// WithAddonHostUIGrantsWithoutAuth lets the UI change host grants although
// authentication is off.
func WithAddonHostUIGrantsWithoutAuth(on bool) AppOption {
	return func(o *ApplicationConfig) { o.AddonHostUIGrantsWithoutAuth = on }
}

// WithAddonHostUIGrantsLocked makes the UI grant store read-only.
func WithAddonHostUIGrantsLocked(locked bool) AppOption {
	return func(o *ApplicationConfig) { o.AddonHostUIGrantsLocked = locked }
}
