// SPDX-License-Identifier: MIT

// Package jfapi is the part of the Jellyfin HTTP API that the Jellyfin
// add-on uses. It talks to one server only: the one the person put on the
// list of the connection. The address never comes from a model. A model can
// name only the connection's own server (the host checks that against the
// list), and every path is built here from fixed segments.
//
// The API key is passed to each call by the caller and is never stored. It
// leaves this package only in the Authorization header, never in the address,
// and an error built here never holds it.
//
// Redirects are refused, so a server cannot send the key to another host. A
// server with a self-signed certificate is reached only when the address names
// the SHA-256 fingerprint of that certificate: the connection then accepts that
// one certificate and no other, and still checks nothing else away.
package jfapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// MaxBody bounds one answer read in memory. A larger answer is an error, not a
// cut document.
const MaxBody = 8 << 20

// defaultWait is how long a limit with no stated end lasts, in seconds.
const defaultWait = 30

// PinPrefix starts the fingerprint in the address of a server.
const PinPrefix = "sha256="

// Errors of a call. None of them holds the key or the text of the answer.
var (
	// ErrUnauthorized means the server refused the key (401).
	ErrUnauthorized = errors.New("the server refused the API key")
	// ErrForbidden means the key may not do this (403).
	ErrForbidden = errors.New("the API key has no permission for this")
	// ErrNotFound means the thing does not exist (404).
	ErrNotFound = errors.New("not found")
	// ErrInvalid means the server could not process the request (other 4xx).
	ErrInvalid = errors.New("the server refused the request")
	// ErrUnavailable means a failure on the way or on the server.
	ErrUnavailable = errors.New("the server is not reachable now")
	// ErrRedirect means the server answered with a redirect, which is refused.
	ErrRedirect = errors.New("the server answered with a redirect, which the add-on does not follow")
	// ErrCertificate means the certificate of the server is not trusted.
	ErrCertificate = errors.New("the certificate of the server is not trusted; for a self-signed certificate, add its SHA-256 fingerprint to the server address on the Jellyfin page")
	// ErrPin means the certificate is not the one the address pins.
	ErrPin = errors.New("the certificate of the server is not the pinned one")
	// ErrUnreadable means a change was accepted but its answer was not readable.
	ErrUnreadable = errors.New("the server accepted the change but its answer was not readable")
	// ErrTooLarge means an answer above MaxBody.
	ErrTooLarge = errors.New("the answer of the server is too large")
)

// Server is the one server of a connection: its address and, for a
// self-signed certificate, the fingerprint to accept.
type Server struct {
	// Base is scheme://host[:port][/prefix] with no trailing slash, no user
	// information, no query and no fragment.
	Base *url.URL
	// Pin is the lower-case hex SHA-256 of the certificate to accept, or "".
	Pin string
}

// ParseServer reads the address a person put on the list of a connection:
// http or https, a host, an optional port and path prefix, and optionally
// #sha256=<64 hex digits> (https only). Anything else is refused.
func ParseServer(key string) (Server, error) {
	key = strings.TrimSpace(key)
	u, err := url.Parse(key)
	if err != nil || key == "" {
		return Server{}, errors.New("the server address is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Server{}, errors.New("the server address must start with http:// or https://")
	}
	if u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" {
		return Server{}, errors.New("the server address is a host with an optional port and path, with no user name, password or query")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || r == '\\' || r == ' ' {
			return Server{}, errors.New("the server address has a character that is not allowed")
		}
	}
	var pin string
	if u.Fragment != "" || strings.Contains(key, "#") {
		f := u.Fragment
		if !strings.HasPrefix(strings.ToLower(f), PinPrefix) {
			return Server{}, errors.New("after # the server address may only hold sha256=<fingerprint>")
		}
		if u.Scheme != "https" {
			return Server{}, errors.New("a certificate fingerprint goes with an https address")
		}
		pin = strings.ToLower(f[len(PinPrefix):])
		pin = strings.ReplaceAll(pin, ":", "")
		if b, err := hex.DecodeString(pin); err != nil || len(b) != sha256.Size {
			return Server{}, errors.New("the fingerprint is 64 hexadecimal digits (SHA-256)")
		}
	}
	if p := u.EscapedPath(); strings.Contains(p, "..") || strings.Contains(p, "//") {
		return Server{}, errors.New("the server address path is not valid")
	}
	b := *u
	b.Fragment, b.RawFragment = "", ""
	b.Path = strings.TrimRight(b.Path, "/")
	b.RawPath = strings.TrimRight(b.RawPath, "/")
	return Server{Base: &b, Pin: pin}, nil
}

// Label is the address without a fingerprint, for a line the person reads.
func (s Server) Label() string {
	if s.Base == nil {
		return ""
	}
	return s.Base.String()
}

// Client calls the API. The zero value is ready.
type Client struct {
	// Now is the clock; a test fixes it.
	Now func() time.Time

	mu      sync.Mutex
	clients map[string]*http.Client
}

// New returns a client.
func New() *Client { return &Client{} }

func (c *Client) client(pin string) *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clients == nil {
		c.clients = map[string]*http.Client{}
	}
	if h, ok := c.clients[pin]; ok {
		return h
	}
	tr := httpclient.HardenedTransport()
	// The add-on talks to the configured server directly, never through a
	// proxy of the environment.
	tr.Proxy = nil
	if pin != "" {
		want, _ := hex.DecodeString(pin)
		// The pinned certificate replaces the chain check: the connection is
		// accepted when the certificate the server presents is the pinned one.
		tr.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // replaced by the fingerprint check in VerifyConnection
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return ErrPin
				}
				sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(sum[:], want) != 1 {
					return ErrPin
				}
				return nil
			},
		}
	}
	h := httpclient.NewWithTimeout(30*time.Second, httpclient.WithTransport(tr))
	c.clients[pin] = h
	return h
}

// Response is the part of an answer a caller needs besides the body.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Request is one call.
type Request struct {
	Method string
	// Path are the segments after the base address. Each is one path element:
	// no slash, no dot-dot, not empty.
	Path  []string
	Query url.Values
	// Body is encoded as JSON when not nil.
	Body any
	// NoAuth sends no key (the public info call).
	NoAuth bool
}

func validKey(key string) bool {
	if key == "" || len(key) > contract.MaxCredentialBytes {
		return false
	}
	for _, r := range key {
		if r < 0x21 || r == 0x7f || r == '"' || r == '\\' {
			return false
		}
	}
	return true
}

func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 200 {
		return false
	}
	return !strings.ContainsAny(s, "/\\?#%\x00") && !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// Do calls the server with the key. A non-2xx answer is an error from the
// list above, or a *contract.ErrRateLimited.
func (c *Client) Do(ctx context.Context, srv Server, key string, r Request) (Response, error) {
	if srv.Base == nil {
		return Response{}, ErrInvalid
	}
	if !r.NoAuth && !validKey(key) {
		return Response{}, ErrUnauthorized
	}
	u := *srv.Base
	elems := make([]string, 0, len(r.Path))
	for _, s := range r.Path {
		if !validSegment(s) {
			return Response{}, ErrInvalid
		}
		elems = append(elems, s)
	}
	u = *u.JoinPath(elems...)
	if len(r.Query) > 0 {
		u.RawQuery = r.Query.Encode()
	}
	var rd io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return Response{}, ErrInvalid
		}
		rd = bytes.NewReader(raw)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return Response{}, ErrInvalid
	}
	// The request goes to the host of the connection and to no other.
	if !strings.EqualFold(req.URL.Host, srv.Base.Host) || req.URL.Scheme != srv.Base.Scheme {
		return Response{}, ErrInvalid
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LocalAI-jellyfin-addon")
	if !r.NoAuth {
		req.Header.Set("Authorization", `MediaBrowser Token="`+key+`"`)
	}
	resp, err := c.client(srv.Pin).Do(req)
	if err != nil {
		return Response{}, classify(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		if method != http.MethodGet && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return Response{Status: resp.StatusCode}, ErrUnreadable
		}
		return Response{}, ErrUnavailable
	}
	if len(body) > MaxBody {
		return Response{}, ErrTooLarge
	}
	out := Response{Status: resp.StatusCode, Header: resp.Header, Body: body}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return out, nil
	}
	return out, c.failure(resp.StatusCode, resp.Header)
}

// classify maps a transport error to one of the errors above. The text of the
// transport error names the address, so it is dropped.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	switch {
	case errors.Is(err, ErrPin):
		return ErrPin
	case errors.Is(err, httpclient.ErrRedirectBlocked):
		return ErrRedirect
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host):
		return ErrCertificate
	}
	var cv *tls.CertificateVerificationError
	if errors.As(err, &cv) {
		return ErrCertificate
	}
	return ErrUnavailable
}

// GetJSON calls the path and decodes the body into v.
func (c *Client) GetJSON(ctx context.Context, srv Server, key string, path []string, query url.Values, v any) error {
	r, err := c.Do(ctx, srv, key, Request{Path: path, Query: query})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) failure(status int, h http.Header) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound, http.StatusGone:
		return ErrNotFound
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		wait := defaultWait
		if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil {
			wait = n
		}
		if status == http.StatusTooManyRequests || h.Get("Retry-After") != "" {
			return &contract.ErrRateLimited{RetryAfter: min(max(wait, 1), contract.MaxRetryAfter)}
		}
		return ErrUnavailable
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return ErrRedirect
	}
	if status >= 500 {
		return ErrUnavailable
	}
	return ErrInvalid
}

// Describe is a fixed sentence for an error of this package, for a log line or
// a refusal. It exists so no caller formats the error of a transport.
func Describe(err error) string {
	for _, e := range []error{ErrUnauthorized, ErrForbidden, ErrNotFound, ErrInvalid, ErrRedirect, ErrCertificate, ErrPin, ErrUnreadable, ErrTooLarge} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return fmt.Sprint(ErrUnavailable)
}
