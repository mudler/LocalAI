// SPDX-License-Identifier: MIT

// Package haapi is the part of the Home Assistant REST API that the add-on
// uses. It speaks only to the address of the connection that the host checked
// for the call. The token goes into the Authorization header and nowhere else:
// an error built here never holds it, a URL or a response body.
//
// Redirects are refused, so a reply cannot send the token to another host.
// The certificate is verified. A person whose instance has a self-signed
// certificate pins its SHA-256 fingerprint in the policy; the check then
// compares the certificate the server sends with that pin, which is stricter
// than a chain check for that one host and does not switch verification off
// for anything else.
package haapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// MaxBody bounds one answer read in memory.
const MaxBody = 16 << 20

const defaultWait = 30

// Errors of a call. None holds the token, the address or the text of the answer.
var (
	ErrBadAddress   = errors.New("the address of the connection is not usable")
	ErrUnauthorized = errors.New("home assistant refused the token")
	ErrForbidden    = errors.New("the token has no permission for this")
	ErrNotFound     = errors.New("not found")
	ErrInvalid      = errors.New("home assistant refused the request")
	ErrUnavailable  = errors.New("home assistant is not reachable now")
	ErrCertificate  = errors.New("the certificate of home assistant is not trusted: if it is self-signed, pin its fingerprint on the add-on page")
	ErrRedirect     = errors.New("home assistant answered with a redirect, which is refused")
	ErrTooLarge     = errors.New("the answer of home assistant is too large")
	ErrUnreadable   = errors.New("home assistant answered, but the answer was not readable")
)

// Target is a checked base address.
type Target struct {
	// Base is the address without a trailing slash, for example https://ha.example:8123.
	Base string
	// HostPort is the lower-case host:port, the key of a pin.
	HostPort string
	// TLS tells whether the scheme is https.
	TLS bool
}

// ParseBase checks the address of a connection: https, or http on a local
// network address only, a host, no credentials in it, no query and no
// fragment. A path prefix is kept, for an instance behind a reverse proxy.
func ParseBase(raw string) (Target, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.Contains(raw, "#") || strings.Contains(raw, "?") {
		return Target{}, ErrBadAddress
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return Target{}, ErrBadAddress
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if host == "" {
		return Target{}, ErrBadAddress
	}
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return Target{}, ErrBadAddress
		}
		if scheme == "http" && !(ip.IsPrivate() || ip.IsLoopback()) {
			return Target{}, ErrBadAddress
		}
	} else if scheme == "http" && !localName(host) {
		return Target{}, ErrBadAddress
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if strings.Contains(path, "..") || strings.Contains(path, "\\") {
		return Target{}, ErrBadAddress
	}
	hp := net.JoinHostPort(host, port)
	return Target{Base: scheme + "://" + hp + path, HostPort: hp, TLS: scheme == "https"}, nil
}

// localName reports a host name that stays on a local network: one label, or
// a name under a suffix used for local names.
func localName(h string) bool {
	if !strings.Contains(h, ".") {
		return true
	}
	for _, s := range []string{".local", ".lan", ".home", ".internal", ".home.arpa", ".localdomain"} {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// Client calls the API. Use New.
type Client struct {
	// Pins returns the pinned fingerprints by host:port. Nil: none.
	Pins func() map[string]string
	// RoundTripper replaces the transport; a test sets it. The pin logic is skipped then.
	RoundTripper http.RoundTripper
	// Timeout bounds one call. Zero: 20 seconds.
	Timeout time.Duration
	// Now is the clock; a test fixes it.
	Now func() time.Time

	mu      sync.Mutex
	clients map[string]*http.Client
}

// New returns a client.
func New() *Client { return &Client{} }

// Fingerprint is the SHA-256 of a DER certificate as 64 lower-case hex digits.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// httpFor returns the client for a target: plain verification, or the pin of
// the host when the policy has one. Clients are cached by host and pin.
func (c *Client) httpFor(t Target) *http.Client {
	pin := ""
	if c.Pins != nil {
		pin = c.Pins()[t.HostPort]
	}
	key := t.HostPort + "|" + pin
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[key]; ok {
		return cl
	}
	var rt http.RoundTripper = c.RoundTripper
	if rt == nil {
		tr := httpclient.HardenedTransport()
		// The instance is reached directly: a proxy of the environment would see the token.
		tr.Proxy = nil
		if t.TLS && pin != "" {
			tr.TLSClientConfig = pinnedConfig(pin)
		}
		rt = tr
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	cl := httpclient.NewWithTimeout(timeout, httpclient.WithTransport(rt))
	if c.clients == nil {
		c.clients = map[string]*http.Client{}
	}
	if len(c.clients) > 64 {
		c.clients = map[string]*http.Client{}
	}
	c.clients[key] = cl
	return cl
}

// pinnedConfig trusts one certificate: the one whose SHA-256 is pin. The chain
// check is replaced by that comparison, and only for the host the pin is for.
func pinnedConfig(pin string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The chain of a self-signed certificate cannot verify; VerifyPeerCertificate
		// below is the check, and it is stricter than a chain check.
		InsecureSkipVerify: true, //nolint:gosec // replaced by the exact pin comparison below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || Fingerprint(raw[0]) != pin {
				return errors.New("certificate does not match the pin")
			}
			return nil
		},
	}
}

// Response is an answer.
type Response struct {
	Status int
	Body   []byte
}

// Get calls a path of the API.
func (c *Client) Get(ctx context.Context, t Target, token, path string, query url.Values) (Response, error) {
	return c.do(ctx, t, token, http.MethodGet, path, query, nil)
}

// GetJSON is Get with the body decoded into v.
func (c *Client) GetJSON(ctx context.Context, t Target, token, path string, query url.Values, v any) error {
	r, err := c.Get(ctx, t, token, path, query)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return ErrUnavailable
	}
	return nil
}

// PostJSON posts body as JSON. A change is never repeated here.
func (c *Client) PostJSON(ctx context.Context, t Target, token, path string, body any, v any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return ErrInvalid
	}
	r, err := c.do(ctx, t, token, http.MethodPost, path, nil, raw)
	if err != nil {
		return err
	}
	if v != nil {
		if err := json.Unmarshal(r.Body, v); err != nil {
			return ErrUnreadable
		}
	}
	return nil
}

// Render renders a fixed template and returns the text. The caller builds the
// template from constants and validated names only: no model text goes in.
func (c *Client) Render(ctx context.Context, t Target, token, template string) (string, error) {
	raw, _ := json.Marshal(map[string]string{"template": template})
	r, err := c.do(ctx, t, token, http.MethodPost, "/api/template", nil, raw)
	if err != nil {
		return "", err
	}
	return string(r.Body), nil
}

func (c *Client) do(ctx context.Context, t Target, token, method, path string, query url.Values, payload []byte) (Response, error) {
	if token == "" {
		return Response{}, ErrUnauthorized
	}
	if t.Base == "" || !strings.HasPrefix(path, "/api") {
		return Response{}, ErrBadAddress
	}
	u := t.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return Response{}, ErrBadAddress
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "LocalAI-home-assistant-addon")
	resp, err := c.httpFor(t).Do(req)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return Response{}, ctx.Err()
		case errors.Is(err, httpclient.ErrRedirectBlocked):
			return Response{}, ErrRedirect
		case isCertError(err):
			return Response{}, ErrCertificate
		}
		// A transport error names the URL; the class is what a caller can use.
		return Response{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return Response{}, ErrUnavailable
	}
	if len(body) > MaxBody {
		return Response{}, ErrTooLarge
	}
	out := Response{Status: resp.StatusCode, Body: body}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return out, nil
	}
	return out, c.failure(resp.StatusCode, resp.Header)
}

func isCertError(err error) bool {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostErr x509.HostnameError
	var certErr *tls.CertificateVerificationError
	return errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostErr) || errors.As(err, &certErr) ||
		strings.Contains(err.Error(), "does not match the pin")
}

func (c *Client) failure(status int, h http.Header) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound, http.StatusGone:
		return ErrNotFound
	case http.StatusTooManyRequests:
		wait := defaultWait
		if v := h.Get("Retry-After"); v != "" {
			if n, ok := atoi(v); ok {
				wait = n
			}
		}
		return &contract.ErrRateLimited{RetryAfter: min(max(wait, 1), contract.MaxRetryAfter)}
	}
	if status >= 500 {
		return ErrUnavailable
	}
	if status >= 300 && status < 400 {
		return ErrRedirect
	}
	return ErrInvalid
}

func atoi(s string) (int, bool) {
	n := 0
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 6 {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// Describe is a fixed sentence for an error of this package.
func Describe(err error) string {
	for _, e := range []error{ErrBadAddress, ErrUnauthorized, ErrForbidden, ErrNotFound, ErrInvalid, ErrCertificate, ErrRedirect, ErrTooLarge, ErrUnreadable} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return ErrUnavailable.Error()
}
