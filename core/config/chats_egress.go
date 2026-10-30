package config

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// EgressPrivatePhrase is what an administrator types to let the Sandbox
// commands reach private addresses (chats_egress_allow_private). It is checked
// by the settings endpoint and never stored.
const EgressPrivatePhrase = "I understand that commands can reach my private network"

// MaxEgressExtraRegistries bounds chats_egress_extra_registries.
const MaxEgressExtraRegistries = 64

// ValidEgressPreset says whether v is a preset of the Sandbox network: off,
// registries, all, or empty for the default (off).
func ValidEgressPreset(v string) bool {
	switch strings.TrimSpace(v) {
	case "", "off", "registries", "all":
		return true
	}
	return false
}

// ParseEgressPorts reads chats_egress_ports: a comma list of ports, as
// chats_web_ports, without the ports of SSH (22) and mail (25). Empty means 80
// and 443.
func ParseEgressPorts(s string) ([]int, error) {
	ports, err := ParsePorts(s)
	if err != nil {
		return nil, err
	}
	for _, p := range ports {
		if p == 22 || p == 25 {
			return nil, fmt.Errorf("port %d is not allowed for the Sandbox network", p)
		}
	}
	return ports, nil
}

// ParseEgressHosts reads chats_egress_extra_registries: a comma list of exact
// host names, each checked with the rules of the guard (no wildcard, no
// numeric tricks, no localhost), at most 64. The names come back canonical.
func ParseEgressHosts(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// Private addresses are allowed here: an internal mirror is a common
	// extra registry, and it still needs the private switch to be reached.
	g := safehttp.NewGuard(safehttp.GuardPolicy{AllowPrivate: true}, nil)
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.Contains(f, "*") {
			return nil, fmt.Errorf("%q: write an exact host name, not a pattern", f)
		}
		h, err := g.CheckHost(f, 443)
		if err != nil {
			return nil, fmt.Errorf("%q is not a host name: %w", f, err)
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	if len(out) > MaxEgressExtraRegistries {
		return nil, fmt.Errorf("at most %d hosts", MaxEgressExtraRegistries)
	}
	return out, nil
}
