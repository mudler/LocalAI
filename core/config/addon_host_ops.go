// SPDX-License-Identifier: MIT
package config

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	addonIDForHostOps = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hostOpPrefix      = regexp.MustCompile(`^[a-z]+\.([a-z]+\.)*$`)
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
