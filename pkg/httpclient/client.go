// Package httpclient provides hardened *http.Client constructors for all
// outbound HTTP traffic in LocalAI.
//
// Direct use of net/http's default client (http.DefaultClient, http.Get,
// http.Post, ...) or a bare http.Client{} is forbidden by lint (forbidigo).
// The reason is GHSA-3mj3-57v2-4636: the standard client follows up to 10
// redirects by default, and on a *cross-host* redirect Go forwards custom
// request headers — including credential headers such as Anthropic's
// x-api-key — to the redirect target. (Go strips Authorization, Cookie and
// WWW-Authenticate cross-host, but NOT arbitrary custom headers.) An attacker
// who can elicit a redirect from an upstream then harvests the credential.
//
// Every client built here refuses redirects by default (see NoRedirect). The
// rare caller that genuinely must follow redirects should opt in with
// WithFollowRedirects, which still strips credential headers on host change.
//
// Streaming note: New() intentionally sets NO client-level Timeout, because a
// global timeout also bounds the response body and would truncate long-lived
// SSE streams (chat completions can stream for minutes). Per-request deadlines
// belong on the request context. Use NewWithTimeout for simple, non-streaming
// request/response calls.
package httpclient

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// Transport-level bounds. These cap connection setup, NOT the response
	// body, so they are safe for streaming responses.
	dialTimeout           = 30 * time.Second
	dialKeepAlive         = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	idleConnTimeout       = 90 * time.Second
	expectContinueTimeout = 1 * time.Second
	maxIdleConns          = 100

	// maxRedirects bounds WithFollowRedirects chains (mirrors the net/http
	// default) so an opt-in follower can't be spun forever by a redirect loop.
	maxRedirects = 10
)

// sensitiveHeaders are credential-bearing request headers that must never be
// replayed to a different host on a redirect. Go already drops the first three
// cross-host; the rest are custom headers Go does not know about. Compared
// case-insensitively via http.Header canonicalisation.
var sensitiveHeaders = []string{
	"Authorization",
	"Www-Authenticate",
	"Cookie",
	"Proxy-Authorization",
	"X-Api-Key",      // Anthropic, and many OpenAI-compatible providers
	"Api-Key",        // Azure OpenAI
	"X-Auth-Token",   // common custom scheme
	"X-Goog-Api-Key", // Google
}

// ErrRedirectBlocked is wrapped by the error NoRedirect returns, so callers can
// distinguish "the upstream tried to redirect us" from other transport errors
// via errors.Is.
var ErrRedirectBlocked = errors.New("httpclient: redirect blocked")

// NoRedirect is an http.Client.CheckRedirect policy that refuses to follow any
// redirect, surfacing it as an error instead. This is the default for clients
// built by New/NewWithTimeout. The error uses URL.Redacted() so userinfo in
// the target URL is not written to logs.
func NoRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("%w: refusing to follow redirect to %s (set httpclient.WithFollowRedirects to opt in)", ErrRedirectBlocked, req.URL.Redacted())
}

// stripAuthOnRedirect follows redirects but deletes credential headers whenever
// the redirect crosses to a different host, closing the cross-host credential
// leak while still allowing same-host or non-authenticated redirect chains.
func stripAuthOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("httpclient: stopped after %d redirects", maxRedirects)
	}
	prev := via[len(via)-1]
	if !sameOrigin(prev.URL, req.URL) {
		for _, h := range sensitiveHeaders {
			req.Header.Del(h)
		}
	}
	return nil
}

// sameOrigin reports whether two URLs share scheme AND host (including port).
// Deliberately strict: a different port or scheme is treated as a different
// origin so credential headers are stripped. This avoids the curl
// CVE-2022-27774 class of bug where ports were ignored and credentials leaked
// to a different service on the same hostname.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// HardenedTransport returns a fresh *http.Transport with a TLS 1.2 floor and
// bounded connection setup. Callers that need to wrap or extend the transport
// (e.g. a credential-injecting RoundTripper) should base it on this rather than
// http.DefaultTransport so the TLS floor and timeouts are preserved.
func HardenedTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: dialKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

type options struct {
	timeout         time.Duration
	transport       http.RoundTripper
	followRedirects bool
}

// Option configures a client built by New.
type Option func(*options)

// WithTimeout sets an overall client Timeout (covers the entire exchange
// including reading the body). Do NOT use this for streaming endpoints; prefer
// a per-request context deadline there. Equivalent to NewWithTimeout.
func WithTimeout(d time.Duration) Option { return func(o *options) { o.timeout = d } }

// WithTransport supplies a custom RoundTripper (e.g. an IP-pinned dialer or a
// credential-injecting wrapper). The caller is responsible for the transport's
// TLS configuration; base it on HardenedTransport to keep the TLS floor.
func WithTransport(rt http.RoundTripper) Option { return func(o *options) { o.transport = rt } }

// WithFollowRedirects opts into following redirects, while still stripping
// credential headers on any cross-host hop. Use only when an endpoint legitimately
// redirects (e.g. some download CDNs) and the request carries a secret.
func WithFollowRedirects() Option { return func(o *options) { o.followRedirects = true } }

// New returns a hardened *http.Client. By default it refuses redirects, sets a
// TLS 1.2 floor, bounds connection setup, and imposes no body deadline (safe
// for streaming). Apply Options to adjust.
func New(opts ...Option) *http.Client {
	o := options{}
	for _, fn := range opts {
		fn(&o)
	}

	rt := o.transport
	if rt == nil {
		rt = HardenedTransport()
	}

	check := NoRedirect
	if o.followRedirects {
		check = stripAuthOnRedirect
	}

	return &http.Client{
		Transport:     rt,
		Timeout:       o.timeout, // zero == no overall deadline (streaming-safe)
		CheckRedirect: check,
	}
}

// NewWithTimeout returns a hardened client with an overall Timeout. Use for
// simple request/response calls; for streaming, use New with a context deadline.
func NewWithTimeout(timeout time.Duration, opts ...Option) *http.Client {
	return New(append([]Option{WithTimeout(timeout)}, opts...)...)
}

// Harden applies the default hardening (refuse redirects, TLS 1.2 floor) to an
// existing client in place, for the cases where a third-party library hands us
// a *http.Client to configure rather than letting us construct one. It returns
// the same client for convenience. A nil client is left nil.
func Harden(c *http.Client) *http.Client {
	if c == nil {
		return nil
	}
	if c.CheckRedirect == nil {
		c.CheckRedirect = NoRedirect
	}
	switch t := c.Transport.(type) {
	case nil:
		c.Transport = HardenedTransport()
	case *http.Transport:
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else if t.TLSClientConfig.MinVersion == 0 {
			t.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	return c
}
