package config

import (
	"context"
	"fmt"
	"sync/atomic"
)

// ProxyKeyResolver returns the upstream key of a remote connection. The
// connections service implements it. It lives here so the backend package can
// resolve a key without importing the service.
type ProxyKeyResolver interface {
	ResolveKey(ctx context.Context, connectionID string) (string, error)
}

type proxyKeyResolverBox struct{ r ProxyKeyResolver }

var proxyKeyResolver atomic.Pointer[proxyKeyResolverBox]

// SetProxyKeyResolver installs the resolver once at startup. Passing nil
// removes it. Reads are safe from any goroutine.
func SetProxyKeyResolver(r ProxyKeyResolver) {
	if r == nil {
		proxyKeyResolver.Store(nil)
		return
	}
	proxyKeyResolver.Store(&proxyKeyResolverBox{r: r})
}

// ResolveProxyConnectionKey returns the key of a connection. The error never
// holds the key and does not say why the lookup failed beyond what the
// resolver reports, so a log line is safe.
func ResolveProxyConnectionKey(ctx context.Context, connectionID string) (string, error) {
	box := proxyKeyResolver.Load()
	if box == nil {
		return "", fmt.Errorf("proxy connection %q: key unavailable", connectionID)
	}
	key, err := box.r.ResolveKey(ctx, connectionID)
	if err != nil {
		return "", fmt.Errorf("proxy connection %q: key unavailable: %w", connectionID, err)
	}
	return key, nil
}
