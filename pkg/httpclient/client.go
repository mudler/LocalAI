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
	"context"
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
	timeout               time.Duration
	responseHeaderTimeout time.Duration
	transport             http.RoundTripper
	followRedirects       bool
	externalOnly          bool
	redirectHosts         map[string]bool
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

// WithResponseHeaderTimeout bounds how long the transport waits for a
// response's headers after the request has been written. Unlike WithTimeout it
// does NOT bound the response body, so it is safe for long transfers: it only
// catches a peer that accepts the connection and then never answers, which
// would otherwise park Do() for the process lifetime.
//
// Opt-in rather than a default in HardenedTransport, because a streaming
// endpoint may legitimately withhold headers until it has something to say (a
// queued or slow-to-first-token completion), and capping that would break
// streaming clients. Use it for request/response traffic and bulk downloads.
func WithResponseHeaderTimeout(d time.Duration) Option {
	return func(o *options) { o.responseHeaderTimeout = d }
}

// WithFollowRedirects opts into following redirects, while still stripping
// credential headers on any cross-host hop. Use only when an endpoint legitimately
// redirects (e.g. some download CDNs) and the request carries a secret.
func WithFollowRedirects() Option { return func(o *options) { o.followRedirects = true } }

// WithRedirectHosts follows a redirect only when the target is an https URL on
// the default port whose host is one of hosts (compared case-insensitively).
// Use it for a fixed upstream that hands its content out through a known CDN,
// such as a GitHub release asset, which github.com answers with a 302 to a
// signed URL on an object-store host. Every hop is checked again: the scheme,
// the host, the external URL policy (no loopback, link-local or private
// address after resolution) and a bound of maxAllowListRedirects hops. On a
// hop to a different origin every request header except a few content
// negotiation headers is dropped, because a credential of the first host can
// have any header name. Unless WithTransport is given, connections to the
// allow-listed hosts dial only validated public addresses; other hosts (the
// URL the caller asked for) keep the default dial, so a library on the local
// network still works.
//
// It takes precedence over WithFollowRedirects.
func WithRedirectHosts(hosts ...string) Option {
	return func(o *options) {
		o.redirectHosts = map[string]bool{}
		for _, h := range hosts {
			o.redirectHosts[strings.ToLower(h)] = true
		}
	}
}

// maxAllowListRedirects bounds WithRedirectHosts chains. A release asset takes
// one hop; a few more leave room for a CDN that moves.
const maxAllowListRedirects = 5

// keptOnCrossOriginRedirect are the request headers WithRedirectHosts keeps
// when a hop changes origin.
var keptOnCrossOriginRedirect = []string{"Accept", "Accept-Language", "User-Agent"}

// allowListRedirect is the CheckRedirect policy of WithRedirectHosts.
func allowListRedirect(hosts map[string]bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxAllowListRedirects {
			return fmt.Errorf("%w: stopped after %d redirects", ErrRedirectBlocked, maxAllowListRedirects)
		}
		host := strings.ToLower(req.URL.Hostname())
		if !strings.EqualFold(req.URL.Scheme, "https") || (req.URL.Port() != "" && req.URL.Port() != "443") || !hosts[host] {
			return fmt.Errorf("%w: refusing to follow redirect to %s://%s, which is not an allowed https host", ErrRedirectBlocked, req.URL.Scheme, req.URL.Host)
		}
		if err := validateRedirectTarget(req); err != nil {
			return fmt.Errorf("%w: %w", ErrRedirectBlocked, err)
		}
		if !sameOrigin(via[len(via)-1].URL, req.URL) {
			kept := http.Header{}
			for _, h := range keptOnCrossOriginRedirect {
				if v := req.Header.Values(h); len(v) > 0 {
					kept[h] = v
				}
			}
			req.Header = kept
		}
		return nil
	}
}

// allowListDialContext dials the hosts of a WithRedirectHosts allow-list
// through the validated dial, and every other host through d.
func allowListDialContext(d *net.Dialer, hosts map[string]bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	validated := validatedDialContext(d)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil && hosts[strings.ToLower(host)] {
			return validated(ctx, network, addr)
		}
		return d.DialContext(ctx, network, addr)
	}
}

// WithExternalOnly restricts a client to hosts that pass the external URL
// policy (see IsPublicIP). Use it for every client that fetches a URL supplied
// by a user. It has two effects:
//
//   - Each redirect hop is validated like the original URL, so a public host
//     cannot bounce the client to a loopback, link-local or private target.
//     It only matters together with WithFollowRedirects.
//   - Unless WithTransport is given, the transport resolves the host itself,
//     rejects it if any resolved address is not public, and dials one of the
//     validated addresses, so a second DNS lookup cannot return a different
//     answer. Environment proxies are not used for such a client, since the
//     proxy address would be what gets validated and dialled.
//
// A caller-supplied transport is left untouched and must enforce the policy
// itself.
func WithExternalOnly() Option { return func(o *options) { o.externalOnly = true } }

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
		t := HardenedTransport()
		if o.externalOnly {
			t.Proxy = nil
			t.DialContext = validatedDialContext(&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: dialKeepAlive,
			})
		} else if len(o.redirectHosts) > 0 {
			t.DialContext = allowListDialContext(&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: dialKeepAlive,
			}, o.redirectHosts)
		}
		rt = t
	}
	// Only an *http.Transport carries the knob; a caller-supplied wrapper
	// (bearer-token RoundTripper, IP-pinned dialer) is left untouched rather
	// than silently ignored-with-a-lie, so it must set the field itself.
	if o.responseHeaderTimeout > 0 {
		if t, ok := rt.(*http.Transport); ok {
			t.ResponseHeaderTimeout = o.responseHeaderTimeout
		}
	}

	check := NoRedirect
	if o.followRedirects {
		check = stripAuthOnRedirect
		if o.externalOnly {
			check = func(req *http.Request, via []*http.Request) error {
				if err := validateRedirectTarget(req); err != nil {
					return err
				}
				return stripAuthOnRedirect(req, via)
			}
		}
	}
	if len(o.redirectHosts) > 0 {
		check = allowListRedirect(o.redirectHosts)
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
