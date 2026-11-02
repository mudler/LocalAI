// SPDX-License-Identifier: MIT

package config

import "github.com/mudler/LocalAI/pkg/egresspolicy"

// EgressPrivatePhrase is what an administrator types to let the Sandbox
// commands reach private addresses (chats_egress_allow_private). It is checked
// by the settings endpoint and never stored.
const EgressPrivatePhrase = egresspolicy.EgressPrivatePhrase

// MaxEgressExtraRegistries bounds chats_egress_extra_registries.
const MaxEgressExtraRegistries = egresspolicy.MaxEgressExtraRegistries

// MaxEgressAllowedHosts bounds chats_egress_allowed_hosts.
const MaxEgressAllowedHosts = egresspolicy.MaxEgressAllowedHosts

// ValidEgressPreset says whether v is a preset of the Sandbox network: off,
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

// EgressAllowedHost is one entry of chats_egress_allowed_hosts: an exact host
// name and, when the entry pins one, the only port it is allowed on. Port 0
// means the ports setting decides.
type EgressAllowedHost = egresspolicy.EgressAllowedHost

// ParseEgressAllowedHosts reads chats_egress_allowed_hosts: a comma list of
// exact host names, each with an optional ":port" that pins the port, at most
// 64. A name is checked with the rules of the guard (no pattern, no scheme or
// path, no numeric tricks), lower-cased, stripped of a trailing dot and
// converted to punycode. An IP literal outside the public internet is refused
// unless allowPrivate (the private switch) is on; even then the list does not
// reach it, since a private address always needs an administrator's rule or
// card answer. The entries come back canonical, without duplicates.
func ParseEgressAllowedHosts(s string, allowPrivate bool) ([]EgressAllowedHost, error) {
	return egresspolicy.ParseEgressAllowedHosts(s, allowPrivate)
}
