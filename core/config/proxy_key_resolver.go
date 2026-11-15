package config

import (
	"context"
	"fmt"
	"sync/atomic"
)

// ProxyKeyResolver returns the upstream key of a remote. The
// remotes service implements it. It lives here so the backend package can
// resolve a key without importing the service.
type ProxyKeyResolver interface {
	ResolveKey(ctx context.Context, remoteID string) (string, error)
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

// ResolveProxyRemoteKey returns the key of a remote. The error never
// holds the key and does not say why the lookup failed beyond what the
// resolver reports, so a log line is safe.
func ResolveProxyRemoteKey(ctx context.Context, remoteID string) (string, error) {
	box := proxyKeyResolver.Load()
	if box == nil {
		return "", fmt.Errorf("proxy remote %q: key unavailable", remoteID)
	}
	key, err := box.r.ResolveKey(ctx, remoteID)
	if err != nil {
		return "", fmt.Errorf("proxy remote %q: key unavailable: %w", remoteID, err)
	}
	return key, nil
}
