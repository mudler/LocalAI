package oci

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"oras.land/oras-go/v2/registry/remote/retry"
)

// maxRedirects is the limit net/http applies when CheckRedirect is nil.
const maxRedirects = 10

// registryHTTPClient is the HTTP client under every ORAS client of this
// package. It keeps the retry policy of retry.DefaultClient and adds a
// redirect rule.
//
// The ORAS auth client sets the Authorization header on the request it
// sends. When the registry answers with a redirect, net/http copies that
// header to the next request if the target has the same host name or is a
// subdomain of it, and it ignores the port. A registry that sends blob reads
// to a storage host on another port or on a subdomain would then receive the
// registry credential. This client removes the header whenever any hop of
// the chain leaves the scheme, host and port of the first request.
func registryHTTPClient() *http.Client {
	return &http.Client{
		Transport:     retry.DefaultClient.Transport,
		CheckRedirect: dropAuthorizationOnCrossOriginRedirect,
	}
}

func dropAuthorizationOnCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("stopped after 10 redirects")
	}
	origin := via[0].URL
	if !sameOrigin(req.URL, origin) {
		req.Header.Del("Authorization")
		return nil
	}
	// Once a hop has left the origin, keep the header off for the rest of
	// the chain, as net/http does for the headers it strips itself.
	for _, hop := range via[1:] {
		if !sameOrigin(hop.URL, origin) {
			req.Header.Del("Authorization")
			return nil
		}
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && hostPort(a) == hostPort(b)
}

// hostPort gives the host and port of u with the scheme's default port
// filled in, so "registry.example" and "registry.example:443" compare equal.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
