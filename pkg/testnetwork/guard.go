// SPDX-License-Identifier: MIT

// Package testnetwork provides an explicit outbound-network guard for tests.
package testnetwork

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

type Guard struct {
	Dialer  net.Dialer
	Dial    func(context.Context, string, string) (net.Conn, error)
	Allowed []netip.Prefix
}

func LocalGuard() *Guard {
	prefixes := []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	guard := &Guard{}
	for _, prefix := range prefixes {
		guard.Allowed = append(guard.Allowed, netip.MustParsePrefix(prefix))
	}
	return guard
}

func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	originalAddress := address
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("test network guard: invalid address %q: %w", address, err)
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", strings.Trim(host, "[]"))
	if err != nil {
		return nil, fmt.Errorf("test network guard: resolve %q: %w", host, err)
	}
	for _, resolved := range addresses {
		if !g.allowed(resolved.Unmap()) {
			return nil, fmt.Errorf("test network guard: public dial blocked: %s (%s)", host, resolved)
		}
	}
	if g.Dial != nil {
		return g.Dial(ctx, network, originalAddress)
	}
	return g.Dialer.DialContext(ctx, network, originalAddress)
}

func (g *Guard) allowed(address netip.Addr) bool {
	for _, prefix := range g.Allowed {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
