// SPDX-License-Identifier: MIT

// Package egresspolicy parses the network policy of commands without application dependencies.
package egresspolicy

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"golang.org/x/net/idna"
)

// EgressPrivatePhrase is what an administrator types to let the commands of an add-on
// reach private addresses (chats_egress_allow_private). It is checked
// by the settings endpoint and never stored.
const EgressPrivatePhrase = "I understand that commands can reach my private network"

// MaxEgressExtraRegistries bounds chats_egress_extra_registries.
const MaxEgressExtraRegistries = 64

// MaxEgressAllowedHosts bounds chats_egress_allowed_hosts.
const MaxEgressAllowedHosts = 64

// ValidEgressPreset says whether v is a preset of the network of commands: off,
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
			return nil, fmt.Errorf("port %d is not allowed for the network of commands", p)
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
	for f := range strings.SplitSeq(s, ",") {
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

// EgressAllowedHost is an exact host, descendant pattern (*.example.com), or
// public-any pattern (*), with an optional pinned port. Port 0
// means the ports setting decides.
type EgressAllowedHost struct {
	Host string
	Port int
}

// String is the canonical entry: the host, then ":port" when pinned. An IPv6
// literal is written in brackets before a port.
func (h EgressAllowedHost) String() string {
	if h.Port == 0 {
		return h.Host
	}
	return net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
}

// splitAllowedHost cuts "host" or "host:port" (also "[v6]:port"). A bare IPv6
// literal has more than one colon and takes no port.
func splitAllowedHost(f string) (host string, port int, err error) {
	hasPort := false
	switch {
	case strings.HasPrefix(f, "["):
		hasPort = strings.Contains(f, "]:")
	default:
		hasPort = strings.Count(f, ":") == 1
	}
	if !hasPort {
		return f, 0, nil
	}
	h, p, err := net.SplitHostPort(f)
	if err != nil {
		return "", 0, fmt.Errorf("write the host and port as host:port")
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("%q is not a port", p)
	}
	if n == 22 || n == 25 {
		return "", 0, fmt.Errorf("port %d is not allowed for the network of commands", n)
	}
	return h, n, nil
}

// ParseEgressAllowedHosts reads chats_egress_allowed_hosts: a comma list of
// exact hosts or whole-label wildcard patterns, with optional pinned ports,
// at most 64. A name is checked with the rules of the guard (no scheme or
// path, no numeric tricks), lower-cased, stripped of a trailing dot and
// converted to punycode. An IP literal outside the public internet is refused
// unless allowPrivate (the private switch) is on; even then the list does not
// reach it, since a private address always needs an administrator's rule or
// card answer. The entries come back canonical, without duplicates.
func ParseEgressAllowedHosts(s string, allowPrivate bool) ([]EgressAllowedHost, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	g := safehttp.NewGuard(safehttp.GuardPolicy{AllowPrivate: allowPrivate}, nil)
	var out []EgressAllowedHost
	seen := map[EgressAllowedHost]bool{}
	for f := range strings.SplitSeq(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.Contains(f, "://") || strings.ContainsAny(f, "/?#@") {
			return nil, fmt.Errorf("%q: write the host name only, without a scheme or path", f)
		}
		host, port, err := splitAllowedHost(f)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", f, err)
		}
		prefix := ""
		if host == "*" {
			e := EgressAllowedHost{Host: "*", Port: port}
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
			continue
		}
		if strings.HasPrefix(host, "*.") {
			prefix = "*."
			host = strings.TrimPrefix(host, prefix)
		}
		if strings.Contains(host, "*") {
			return nil, fmt.Errorf("%q: only * or *.domain patterns are allowed", f)
		}
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if ascii, err := idna.Lookup.ToASCII(host); err == nil {
			host = ascii
		} else if !isASCII(host) {
			return nil, fmt.Errorf("%q is not a host name: %w", f, err)
		}
		h, err := g.CheckHost(host, 443)
		if err != nil {
			return nil, fmt.Errorf("%q is not a host name: %w", f, err)
		}
		if a, err := netip.ParseAddr(h); err == nil && !allowPrivate && !safehttp.Allowed(a, false) {
			return nil, fmt.Errorf("%q is a private address: turn on the private network switch first", f)
		}
		if prefix != "" {
			if _, err := netip.ParseAddr(h); err == nil || !dnsLabels(h) {
				return nil, fmt.Errorf("%q: a descendant pattern requires a DNS name", f)
			}
		}
		e := EgressAllowedHost{Host: prefix + h, Port: port}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	if len(out) > MaxEgressAllowedHosts {
		return nil, fmt.Errorf("at most %d hosts", MaxEgressAllowedHosts)
	}
	return out, nil
}

// Pattern distinguishes wildcard grants from the legacy exact-host exception.
func (h EgressAllowedHost) Pattern() bool { return strings.Contains(h.Host, "*") }

// Matches compares canonical names at label boundaries. Public-address checks
// belong to the caller, after resolution; a textual match never permits a dial.
func (h EgressAllowedHost) Matches(host string, port int) bool {
	if h.Port != 0 && h.Port != port {
		return false
	}
	g := safehttp.NewGuard(safehttp.GuardPolicy{AllowPrivate: true}, nil)
	name, err := g.CheckHost(host, 443)
	if err != nil {
		return false
	}
	if !h.Pattern() {
		return h.Host == name
	}
	if a, err := netip.ParseAddr(name); err == nil {
		return h.Host == "*" && safehttp.Allowed(a, false)
	}
	if !dnsLabels(name) || name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return false
	}
	if h.Host == "*" {
		return true
	}
	return strings.HasPrefix(h.Host, "*.") && strings.HasSuffix(name, h.Host[1:])
}

func dnsLabels(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// ParsePorts reads a comma list of TCP ports, as the chats_web_ports setting
// holds it. An empty list means 80 and 443.
func ParsePorts(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []int{80, 443}, nil
	}
	var out []int
	seen := map[int]bool{}
	for f := range strings.SplitSeq(s, ",") {
		f = strings.TrimSpace(f)
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%q is not a port (1 to 65535)", f)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > 32 {
		return nil, fmt.Errorf("at most 32 ports")
	}
	return out, nil
}
