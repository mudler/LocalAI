package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"golang.org/x/net/idna"
)

// EgressPrivatePhrase is what an administrator types to let the Sandbox
// commands reach private addresses (chats_egress_allow_private). It is checked
// by the settings endpoint and never stored.
const EgressPrivatePhrase = "I understand that commands can reach my private network"

// MaxEgressExtraRegistries bounds chats_egress_extra_registries.
const MaxEgressExtraRegistries = 64

// MaxEgressAllowedHosts bounds chats_egress_allowed_hosts.
const MaxEgressAllowedHosts = 64

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

// EgressAllowedHost is one entry of chats_egress_allowed_hosts: an exact host
// name and, when the entry pins one, the only port it is allowed on. Port 0
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
	if strings.Contains(h.Host, ":") {
		return "[" + h.Host + "]:" + strconv.Itoa(h.Port)
	}
	return h.Host + ":" + strconv.Itoa(h.Port)
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
		return "", 0, fmt.Errorf("port %d is not allowed for the Sandbox network", n)
	}
	return h, n, nil
}

// ParseEgressAllowedHosts reads chats_egress_allowed_hosts: a comma list of
// exact host names, each with an optional ":port" that pins the port, at most
// 64. A name is checked with the rules of the guard (no pattern, no scheme or
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
		if strings.Contains(f, "*") {
			return nil, fmt.Errorf("%q: write an exact host name, not a pattern", f)
		}
		if strings.Contains(f, "://") || strings.ContainsAny(f, "/?#@") {
			return nil, fmt.Errorf("%q: write the host name only, without a scheme or path", f)
		}
		host, port, err := splitAllowedHost(f)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", f, err)
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
		e := EgressAllowedHost{Host: h, Port: port}
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

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
