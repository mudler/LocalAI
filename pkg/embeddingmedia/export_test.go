// SPDX-License-Identifier: MIT
package embeddingmedia

import (
	"context"
	"crypto/x509"
	"net"
	"net/netip"
	"time"
)

// TestHooks injects name resolution, dialing and trust roots. It exists only
// in test builds, so production code has no way to relax the address policy.
type TestHooks struct {
	Resolve      func(ctx context.Context, host string) ([]netip.Addr, error)
	Dial         func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error)
	RootCAs      *x509.CertPool
	FetchTimeout time.Duration
}

func NewTestAcquirer(h TestHooks) *Acquirer {
	return newAcquirer(acquirerConfig{resolve: h.Resolve, dial: h.Dial, rootCAs: h.RootCAs, fetchTimeout: h.FetchTimeout})
}

func IsApprovedAddress(a netip.Addr) bool { return isApprovedAddress(a) }

// ProxyDisabled reports structural hardening that the
// process-wide environment proxy cache makes unreliable to observe by traffic.
func ProxyDisabled(a *Acquirer) bool {
	return a.transport.Proxy == nil && a.transport.DisableKeepAlives
}
