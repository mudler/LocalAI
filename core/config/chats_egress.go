// SPDX-License-Identifier: MIT

package config

import "github.com/mudler/LocalAI/pkg/egresspolicy"

// EgressPrivatePhrase is what an administrator types to let the commands of an add-on
// reach private addresses (chats_egress_allow_private). It is checked
// by the settings endpoint and never stored.
const EgressPrivatePhrase = egresspolicy.EgressPrivatePhrase

// MaxEgressExtraRegistries bounds chats_egress_extra_registries.
const MaxEgressExtraRegistries = egresspolicy.MaxEgressExtraRegistries

// MaxEgressAllowedHosts bounds chats_egress_allowed_hosts.
const MaxEgressAllowedHosts = egresspolicy.MaxEgressAllowedHosts

// ValidEgressPreset says whether v is a preset of the network of commands: off,
// registries, all, or empty for the default (off).
func ValidEgressPreset(v string) bool {
	return egresspolicy.ValidEgressPreset(v)
}

// ParseEgressPorts reads chats_egress_ports: a comma list of ports, as
// chats_web_ports, without the ports of SSH (22) and mail (25). Empty means 80
// and 443.
func ParseEgressPorts(s string) ([]int, error) {
	return egresspolicy.ParseEgressPorts(s)
}

// ParseEgressHosts reads chats_egress_extra_registries: a comma list of exact
// host names, each checked with the rules of the guard (no wildcard, no
// numeric tricks, no localhost), at most 64. The names come back canonical.
func ParseEgressHosts(s string) ([]string, error) {
	return egresspolicy.ParseEgressHosts(s)
}

// EgressAllowedHost is a canonical exact host or whole-label wildcard with an optional port.
type EgressAllowedHost = egresspolicy.EgressAllowedHost

// ParseEgressAllowedHosts parses bounded exact hosts and whole-label wildcards.
// Wildcards never grant private access, even with allowPrivate enabled.
func ParseEgressAllowedHosts(s string, allowPrivate bool) ([]EgressAllowedHost, error) {
	return egresspolicy.ParseEgressAllowedHosts(s, allowPrivate)
}
