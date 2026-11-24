package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The external URL policy lives here, below pkg/utils, so the redirect check
// and the dial path of a client can enforce it without an import cycle.
// utils.ValidateExternalURL and utils.IsPublicIP delegate to these functions.

// nonPublicNets lists ranges that are not covered by the net.IP helper
// predicates (IsPrivate, IsLoopback, ...) but are not routable on the public
// internet either.
var nonPublicNets = mustParseCIDRs(
	"0.0.0.0/8",     // "this network", not only the single unspecified address
	"100.64.0.0/10", // RFC 6598 shared address space (carrier-grade NAT, overlay VPNs)
	"192.0.0.0/24",  // RFC 6890 IETF protocol assignments
	"198.18.0.0/15", // RFC 2544 benchmarking
	"240.0.0.0/4",   // reserved, includes the limited broadcast address
	"100::/64",      // RFC 6666 discard-only prefix
	"2001:db8::/32", // documentation prefix
)

// nat64Prefix is the well-known NAT64 prefix (RFC 6052). The last four bytes
// are an IPv4 address that must pass the IPv4 rules too.
var nat64Prefix = mustParseCIDRs("64:ff9b::/96")[0]

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

// IsPublicIP reports whether ip refers to a host on the public internet, i.e.
// not loopback, link-local, private (RFC 1918 / RFC 4193), shared address
// space (RFC 6598), multicast, unspecified or otherwise reserved. An
// IPv4-mapped or NAT64-embedded IPv4 address is checked as that IPv4 address.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	} else if nat64Prefix.Contains(ip) {
		return IsPublicIP(net.IP(ip.To16()[12:]))
	}

	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() {
		return false
	}
	for _, n := range nonPublicNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// ValidateExternalURL checks that the given URL does not point to a private,
// loopback, link-local, or otherwise internal network address. This prevents
// Server-Side Request Forgery (SSRF) where a user-supplied URL could be used
// to probe internal services or cloud metadata endpoints.
func ValidateExternalURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme: %s", scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL has no hostname")
	}

	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("requests to internal hosts are not allowed")
	}
	if lower == "metadata.google.internal" || lower == "instance-data" {
		return fmt.Errorf("requests to cloud metadata services are not allowed")
	}

	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return fmt.Errorf("unable to parse resolved IP: %s", ipStr)
		}
		if !IsPublicIP(ip) {
			return fmt.Errorf("requests to internal network addresses are not allowed")
		}
	}
	return nil
}

// ErrNonPublicAddress is wrapped by the error a validated dial returns when a
// host resolves to an address outside the external URL policy.
var ErrNonPublicAddress = errors.New("httpclient: requests to internal network addresses are not allowed")

// resolver is swapped in tests.
var resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
} = net.DefaultResolver

// validatedDialContext returns a DialContext that resolves the host itself,
// rejects the connection if ANY resolved address is not public, and then dials
// the validated addresses directly. The address that was checked is therefore
// the address that is connected to, which closes the window in which a second
// DNS lookup could return a different (internal) answer. net/http keeps the
// original host for the Host header and TLS server name, because only the dial
// address is replaced.
func validatedDialContext(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var ips []net.IP
		if lit := net.ParseIP(host); lit != nil {
			ips = []net.IP{lit}
		} else {
			addrs, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve hostname: %w", err)
			}
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no addresses found for %s", host)
		}
		for _, ip := range ips {
			if !IsPublicIP(ip) {
				return nil, ErrNonPublicAddress
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}
}

// validateRedirectTarget applies the external URL policy to a redirect hop.
func validateRedirectTarget(req *http.Request) error {
	if err := ValidateExternalURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect URL validation failed: %w", err)
	}
	return nil
}
