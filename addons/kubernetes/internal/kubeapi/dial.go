// SPDX-License-Identifier: MIT
package kubeapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// NormalizeServer refuses a server written as a local IP address, but a host
// name can resolve to one: localhost, a name whose DNS record points at
// 127.0.0.1 or at a cloud metadata address, or a form that only the system
// resolver reads as an address (2130706433, 0x7f.0.0.1). So every connection
// of a client checks the address it dials, after DNS, and refuses the same
// addresses. The add-on connects directly, with no proxy, so the address
// checked is the address of the API server.

// dialer is the dialer of every connection of a client. allowLoopback is for
// the tests that run an API server on the loopback interface; nothing a
// person or the manifest sets reaches it.
func dialer(allowLoopback bool) *net.Dialer {
	return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error { return checkDial(address, allowLoopback) }}
}

// checkDial refuses an address that blocked reports. An address it cannot
// read is refused too.
func checkDial(address string, allowLoopback bool) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %q is not an address", ErrLocalServer, address)
	}
	ip := ap.Addr().WithZone("").Unmap()
	if allowLoopback && ip.IsLoopback() {
		return nil
	}
	if blocked(ip) {
		return fmt.Errorf("%w: the API server resolves to %s", ErrLocalServer, ip)
	}
	return nil
}

// nat64 are the NAT64 prefixes (RFC 6052 and RFC 8215), which carry an IPv4
// address in their last 32 bits.
var nat64 = []netip.Prefix{netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48")}

// metadata are cloud metadata endpoints outside the link-local ranges: the
// IPv6 endpoint of AWS and the endpoint of Alibaba Cloud.
var metadata = []netip.Prefix{netip.MustParsePrefix("fd00:ec2::254/128"), netip.MustParsePrefix("100.100.100.200/32")}

// blocked reports an address of the host or of its local link: loopback,
// link-local, unspecified, multicast, 0.0.0.0/8, a cloud metadata endpoint,
// or an IPv4 address of these kinds carried in a NAT64 or an
// IPv4-compatible IPv6 address.
func blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if localAddr(ip) || (ip.Is4() && ip.As4()[0] == 0) {
		return true
	}
	for _, p := range metadata {
		if p.Contains(ip) {
			return true
		}
	}
	if !ip.Is6() {
		return false
	}
	b := ip.As16()
	inner := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	for _, p := range nat64 {
		if p.Contains(ip) {
			return blocked(inner)
		}
	}
	if [12]byte(b[:12]) == ([12]byte{}) {
		// ::a.b.c.d, the IPv4-compatible form.
		return blocked(inner)
	}
	return false
}

// noProxy makes a client connect directly: an environment proxy would make
// the dial check see the address of the proxy instead of the API server.
func noProxy(*http.Request) (*url.URL, error) { return nil, nil }

// checkResolved resolves the host of a URL and refuses it when any of its
// addresses is blocked. It is for the WebSocket exec, whose dialer cannot be
// replaced: the connection itself resolves again, so a name whose DNS answer
// changes in between can still pass. The SPDY fallback and every other request
// check the address they dial.
func checkResolved(ctx context.Context, host string, allowLoopback bool) error {
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%w: the server has no host", ErrLocalServer)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if err := checkDial(netip.AddrPortFrom(ip, 443).String(), allowLoopback); err != nil {
			return err
		}
	}
	return nil
}
