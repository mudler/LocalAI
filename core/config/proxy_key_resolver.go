package config

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
)

// ProxyRemoteKey is what a remote gives a model load: its key and the
// address the key belongs to. Base is the address the entries of the remote
// are built from (the same value the remote service derives for
// proxy.upstream_url), so a generated entry always lies below it.
type ProxyRemoteKey struct {
	Key  string
	Kind string
	Base string
}

// ProxyKeyResolver returns the key of a remote and the address it is bound
// to. The remotes service implements it. It lives here so the backend
// package can resolve a key without importing the service.
type ProxyKeyResolver interface {
	ResolveKey(ctx context.Context, remoteID string) (ProxyRemoteKey, error)
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

// ResolveProxyRemoteKey returns the key of a remote for a model whose
// proxy.upstream_url is upstreamURL. It refuses when that address is not
// below the address of the remote: a model config that names a remote must
// not carry its key to another server. The error never holds the key and
// does not say why the lookup failed beyond what the resolver reports, so a
// log line is safe.
func ResolveProxyRemoteKey(ctx context.Context, remoteID, upstreamURL string) (string, error) {
	box := proxyKeyResolver.Load()
	if box == nil {
		return "", fmt.Errorf("proxy remote %q: key unavailable", remoteID)
	}
	rk, err := box.r.ResolveKey(ctx, remoteID)
	if err != nil {
		return "", fmt.Errorf("proxy remote %q: key unavailable: %w", remoteID, err)
	}
	if !UpstreamWithinBase(upstreamURL, rk.Base) {
		return "", fmt.Errorf("proxy remote %q: proxy.upstream_url is not an address of this %s remote, so its key is not sent; set proxy.upstream_url to an address below the address of the remote or remove proxy.remote", remoteID, rk.Kind)
	}
	return rk.Key, nil
}

// UpstreamWithinBase reports whether upstream lies below base: the same
// scheme, the same host (without case) and port (a missing port is the
// default port of the scheme), no user information, and a path that is the
// path of base or below it. A path with "." or ".." segments is refused, so
// it cannot climb out of the base path.
func UpstreamWithinBase(upstream, base string) bool {
	u, err := url.Parse(strings.TrimSpace(upstream))
	if err != nil {
		return false
	}
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	if u.User != nil || b.User != nil || u.Opaque != "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != strings.ToLower(b.Scheme) || (scheme != "http" && scheme != "https") {
		return false
	}
	if !strings.EqualFold(u.Hostname(), b.Hostname()) || u.Hostname() == "" {
		return false
	}
	if effectivePort(u) != effectivePort(b) {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	basePath := strings.TrimRight(b.Path, "/")
	path := u.Path
	return basePath == "" || path == basePath || strings.HasPrefix(path, basePath+"/")
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
