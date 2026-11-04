// Package clientip finds the address of the real client of an HTTP request.
//
// Forwarding headers such as X-Forwarded-For can be set by anyone. They are
// only meaningful when the TCP peer is a reverse proxy the operator trusts.
// This package is the single place that decides that. Everything in the
// server that needs a client address (access log, rate limits, traces, echo's
// Context.RealIP) must go through a Resolver.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/labstack/echo/v4"
)

// Resolver resolves the client address of a request. The zero value, and a
// Resolver built from an empty list, trusts nobody: it always returns the TCP
// peer.
type Resolver struct {
	trusted []netip.Prefix
}

// Parse builds a Resolver from a comma-separated list of IP addresses and
// CIDR ranges. An empty string gives a Resolver that trusts nobody.
func Parse(list string) (*Resolver, error) {
	var items []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			items = append(items, s)
		}
	}
	return New(items)
}

// New builds a Resolver from IP addresses and CIDR ranges.
func New(items []string) (*Resolver, error) {
	r := &Resolver{}
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(s, "/") {
			pp, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: %w", s, err)
			}
			p = pp
		} else {
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: not an IP address or CIDR", s)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		// Treat ::ffff:a.b.c.d like a.b.c.d, in both the list and the lookups.
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return nil, fmt.Errorf("trusted proxy %q: IPv4-mapped range must be at least /96", s)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

// Enabled reports whether any proxy is trusted.
func (r *Resolver) Enabled() bool { return r != nil && len(r.trusted) > 0 }

func (r *Resolver) isTrusted(a netip.Addr) bool {
	if r == nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// TrustsPeer reports whether the TCP peer of req is a trusted proxy. Only then
// may the forwarding headers of the request (X-Forwarded-For, -Proto, -Host,
// -Prefix, Forwarded) be believed. A nil or empty Resolver trusts nobody.
func (r *Resolver) TrustsPeer(req *http.Request) bool {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	return err == nil && r.isTrusted(peer)
}

// Resolve returns the client address of req, without port. When the TCP peer
// is not trusted, or the list is empty, that is the peer. When it is trusted,
// the result is the right-most X-Forwarded-For entry that is not itself a
// trusted proxy. If there is no X-Forwarded-For, X-Real-IP is used. If an
// entry cannot be parsed, the walk stops and the closest trusted hop is
// returned, because nothing left of a forged or broken entry can be believed.
func (r *Resolver) Resolve(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !r.isTrusted(peer) {
		return host
	}
	hop := peer.Unmap().WithZone("")

	var entries []string
	for _, v := range req.Header.Values(echo.HeaderXForwardedFor) {
		entries = append(entries, strings.Split(v, ",")...)
	}
	if len(entries) == 0 {
		if a, ok := parseAddr(req.Header.Get(echo.HeaderXRealIP)); ok {
			return a.String()
		}
		return hop.String()
	}
	for i := len(entries) - 1; i >= 0; i-- {
		a, ok := parseAddr(entries[i])
		if !ok {
			return hop.String()
		}
		if !r.isTrusted(a) {
			return a.String()
		}
		hop = a
	}
	// Every hop is a trusted proxy: the client is the left-most one.
	return hop.String()
}

// Extractor returns an echo.IPExtractor backed by the Resolver. Set it as
// Echo.IPExtractor so Context.RealIP stops trusting raw headers.
func (r *Resolver) Extractor() echo.IPExtractor {
	return func(req *http.Request) string { return r.Resolve(req) }
}

// parseAddr parses one forwarding header value: an IP, optionally with a
// port, in brackets for IPv6, with an optional zone.
func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}
