// SPDX-License-Identifier: MIT
package config

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultAddonHostOps names the add-ons that may call host operations, and
// the operation prefixes each may call. It is a host setting: neither a
// manifest field nor a model argument can add to it.
const DefaultAddonHostOps = "fine-tuning=finetune.,quantization=quantize."

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
			return nil, fmt.Errorf("invalid host operation entry %q: want addon-id=op-prefix, such as fine-tuning=finetune.", item)
		}
		out[id] = append(out[id], prefix)
	}
	return out, nil
}

// WithAddonHostOps sets which add-ons may call which host operations.
// Without this option no add-on gets a host operation endpoint; the run
// command passes the default setting.
func WithAddonHostOps(ops map[string][]string) AppOption {
	return func(o *ApplicationConfig) { o.AddonHostOps = ops }
}
