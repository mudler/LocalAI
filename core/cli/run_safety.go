package cli

import (
	"fmt"
	"net"
	"strings"

	"github.com/mudler/LocalAI/pkg/utils"
)

// interfaceAddrsFn is the host-interface enumeration call. Tests swap it to
// simulate cloud-VM, home-LAN, and Tailscale-only topologies without poking
// the real network stack.
var interfaceAddrsFn = net.InterfaceAddrs

// requireAuthOrTrustedBind fails closed when the server would otherwise bind a
// public-internet-reachable address with no authentication configured. Loopback,
// RFC 1918, ULA, link-local, and CGNAT (Tailscale's default range) are all
// trusted. Wildcard binds are trusted only when every host interface is.
//
// Operators with an external gating layer (e.g. a reverse proxy that enforces
// auth) can opt out via --allow-insecure-public-bind.
func requireAuthOrTrustedBind(address string, authConfigured, allowInsecurePublicBind bool) error {
	if authConfigured || allowInsecurePublicBind {
		return nil
	}
	if isTrustedBind(address) {
		return nil
	}
	return fmt.Errorf(`refusing to start: API bound to public address %q with no authentication configured.

When auth is disabled, the server has no idea who is calling it — every model,
gallery install, settings change, and admin endpoint is reachable by anyone
who can connect to the port. That is acceptable on a loopback, LAN, or VPN
address but not on a public IP.

Pick one:
  1. Bind to a private/LAN/VPN interface only (e.g. --address 10.0.0.5:8080)
  2. Enable user authentication:   --auth (or LOCALAI_AUTH=true), then sign in
  3. Set a static API key:         --api-keys <key> (LOCALAI_API_KEY=<key>)
  4. Allow the public bind anyway: --allow-insecure-public-bind (only when an
                                   external system is gating access to this
                                   listener)`, address)
}

// isTrustedBind reports whether `address` binds only to addresses that are
// local, on a private LAN, or on a VPN. Hostnames it can't classify cleanly
// are rejected.
func isTrustedBind(address string) bool {
	if address == "" {
		return false
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return allInterfacesTrusted()
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			return allInterfacesTrusted()
		}
		return isPrivateOrLocalIP(ip)
	}
	// Hostname — every resolved address must be trusted. A name resolving to
	// a mix of public and private addresses fails closed.
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || !isPrivateOrLocalIP(ip) {
			return false
		}
	}
	return true
}

// isPrivateOrLocalIP returns true for loopback, RFC 1918 / RFC 4193 private,
// link-local, and RFC 6598 CGNAT addresses. CGNAT (100.64/10) gets the
// special case because the Go stdlib doesn't classify it as private but
// Tailscale and similar overlay VPNs hand them out.
func isPrivateOrLocalIP(ip net.IP) bool {
	if ip.IsUnspecified() {
		return false
	}
	if !utils.IsPublicIP(ip) {
		return true
	}
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && (ip4[1]&0xc0) == 64
}

// allInterfacesTrusted reports whether every IP assigned to a local interface
// is private/local. A wildcard bind on a host with even one public interface
// is genuinely exposing that public interface.
//
// Returns false on enumeration failure or when the host has no addresses
// at all — we can't prove the bind is safe.
func allInterfacesTrusted() bool {
	addrs, err := interfaceAddrsFn()
	if err != nil {
		return false
	}
	sawAny := false
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsUnspecified() {
			continue
		}
		sawAny = true
		if !isPrivateOrLocalIP(ip) {
			return false
		}
	}
	return sawAny
}
