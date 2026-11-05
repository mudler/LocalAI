// SPDX-License-Identifier: MIT

// Package kvmd is the part of the PiKVM (kvmd) HTTP API that the PiKVM add-on
// uses. It talks to one device only: the one the person put on the list of the
// connection. The address never comes from a model, and every path is built
// here from fixed segments.
//
// The credential is "user:password", split at the first colon. It is passed to
// each call by the caller and never stored. It leaves this package only in the
// X-KVMD-User and X-KVMD-Passwd headers, and an error built here never holds
// it. API reference: https://docs.pikvm.org/api/
//
// Redirects are refused and no proxy is used. A device with the usual
// self-signed certificate is reached only when the address names the SHA-256
// fingerprint of that certificate: the connection then accepts that one
// certificate and no other. Verification is never switched off.
package kvmd

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
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// MaxBody bounds one answer read in memory.
const MaxBody = 1 << 20

const defaultWait = 30

// PinPrefix starts the fingerprint in the address of a device.
const PinPrefix = "sha256="

// Errors of a call. None of them holds the credential or the text of an answer.
var (
	ErrBadAddress    = errors.New("the device address is not valid: use https://host[:port] with an optional #sha256=<fingerprint>; plain http only for an address on your own network")
	ErrBadCredential = errors.New("the credential must be written user:password")
	ErrUnauthorized  = errors.New("the device refused the user name or password (a one-time code is not supported)")
	ErrForbidden     = errors.New("the user has no permission for this")
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("the device refused the request")
	ErrBusy          = errors.New("the device is busy; nothing was sent")
	ErrUnavailable   = errors.New("the device is not reachable now")
	ErrRedirect      = errors.New("the device answered with a redirect, which the add-on does not follow")
	ErrCertificate   = errors.New("the certificate of the device is not trusted; for a self-signed certificate, add its SHA-256 fingerprint to the device address on the PiKVM page")
	ErrPin           = errors.New("the certificate of the device is not the pinned one")
	ErrTooLarge      = errors.New("the answer of the device is too large")
	ErrUnreadable    = errors.New("the device answered but its answer was not readable")
)

// Device is the one device of a connection.
type Device struct {
	// Base is scheme://host[:port][/prefix] with no trailing slash.
	Base *url.URL
	// Pin is the lower-case hex SHA-256 of the certificate to accept, or "".
	Pin string
}

// Key is the address without the fingerprint, in one canonical form: it names
// the device in the settings of the add-on.
func (d Device) Key() string {
	if d.Base == nil {
		return ""
	}
	u := *d.Base
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

// ParseDevice reads the address a person put on the list of a connection.
func ParseDevice(raw string) (Device, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return Device{}, ErrBadAddress
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return Device{}, ErrBadAddress
	}
	if u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" {
		return Device{}, ErrBadAddress
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f || r == '\\' || r == ' ' {
			return Device{}, ErrBadAddress
		}
	}
	var pin string
	if u.Fragment != "" || strings.Contains(raw, "#") {
		f := u.Fragment
		if !strings.HasPrefix(strings.ToLower(f), PinPrefix) || u.Scheme != "https" {
			return Device{}, ErrBadAddress
		}
		pin = strings.ReplaceAll(strings.ToLower(f[len(PinPrefix):]), ":", "")
		if b, err := hex.DecodeString(pin); err != nil || len(b) != sha256.Size {
			return Device{}, ErrBadAddress
		}
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return Device{}, ErrBadAddress
		}
		// The credential travels in headers: plain http only on a local network.
		if u.Scheme == "http" && !(ip.IsPrivate() || ip.IsLoopback()) {
			return Device{}, ErrBadAddress
		}
	} else if u.Scheme == "http" && !localName(host) {
		return Device{}, ErrBadAddress
	}
	if p := u.EscapedPath(); strings.Contains(p, "..") || strings.Contains(p, "//") {
		return Device{}, ErrBadAddress
	}
	b := *u
	b.Fragment, b.RawFragment = "", ""
	b.Path = strings.TrimRight(b.Path, "/")
	b.RawPath = strings.TrimRight(b.RawPath, "/")
	return Device{Base: &b, Pin: pin}, nil
}

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

// SplitCredential splits "user:password" at the first colon. The password may
// hold colons. Neither part may be empty or hold a control character.
func SplitCredential(cred string) (user, pass string, err error) {
	user, pass, ok := strings.Cut(cred, ":")
	if !ok || user == "" || pass == "" || len(cred) > contract.MaxCredentialBytes {
		return "", "", ErrBadCredential
	}
	for _, s := range []string{user, pass} {
		if strings.ContainsFunc(s, unicode.IsControl) {
			return "", "", ErrBadCredential
		}
	}
	return user, pass, nil
}

// Client calls the API. The zero value is ready.
type Client struct {
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
	// The add-on talks to the configured device directly, never through a proxy.
	tr.Proxy = nil
	if pin != "" {
		want, _ := hex.DecodeString(pin)
		// The pinned certificate replaces the chain check: a self-signed
		// certificate has no chain. The connection is accepted only when the
		// certificate the device presents is the pinned one.
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

// Request is one call.
type Request struct {
	Method string
	// Path are the segments after the base address, each one path element.
	Path  []string
	Query url.Values
	// Text is sent as the plain text body when not empty.
	Text string
}

func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 100 {
		return false
	}
	return !strings.ContainsAny(s, "/\\?#%\x00") && !strings.ContainsFunc(s, unicode.IsControl)
}

// Do calls the device and returns the "result" member of the answer.
// Non-2xx answers are errors from the list above, or *contract.ErrRateLimited.
func (c *Client) Do(ctx context.Context, dev Device, cred string, r Request) (json.RawMessage, error) {
	if dev.Base == nil {
		return nil, ErrBadAddress
	}
	user, pass, err := SplitCredential(cred)
	if err != nil {
		return nil, ErrUnauthorized
	}
	elems := make([]string, 0, len(r.Path))
	for _, s := range r.Path {
		if !validSegment(s) {
			return nil, ErrInvalid
		}
		elems = append(elems, s)
	}
	u := *dev.Base.JoinPath(elems...)
	if len(r.Query) > 0 {
		u.RawQuery = r.Query.Encode()
	}
	var rd io.Reader
	if r.Text != "" {
		rd = strings.NewReader(r.Text)
	} else if r.Method == http.MethodPost {
		rd = bytes.NewReader(nil)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, ErrInvalid
	}
	if !strings.EqualFold(req.URL.Host, dev.Base.Host) || req.URL.Scheme != dev.Base.Scheme {
		return nil, ErrInvalid
	}
	if r.Text != "" {
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LocalAI-pikvm-addon")
	req.Header.Set("X-KVMD-User", user)
	req.Header.Set("X-KVMD-Passwd", pass)
	resp, err := c.client(dev.Pin).Do(req)
	if err != nil {
		return nil, classify(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		if method != http.MethodGet && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, ErrUnreadable
		}
		return nil, ErrUnavailable
	}
	if len(body) > MaxBody {
		return nil, ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, failure(resp.StatusCode, resp.Header)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &env) != nil || !env.OK {
		if method != http.MethodGet {
			return nil, ErrUnreadable
		}
		return nil, ErrUnavailable
	}
	return env.Result, nil
}

// GetJSON calls and decodes the result into v.
func (c *Client) GetJSON(ctx context.Context, dev Device, cred string, path []string, q url.Values, v any) error {
	raw, err := c.Do(ctx, dev, cred, Request{Path: path, Query: q})
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, v) != nil {
		return ErrUnavailable
	}
	return nil
}

func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	var cv *tls.CertificateVerificationError
	switch {
	case errors.Is(err, ErrPin):
		return ErrPin
	case errors.Is(err, httpclient.ErrRedirectBlocked):
		return ErrRedirect
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host), errors.As(err, &cv):
		return ErrCertificate
	}
	return ErrUnavailable
}

func failure(status int, h http.Header) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		// kvmd answers 403 for bad credentials, a bad one-time code included.
		return ErrUnauthorized
	case http.StatusNotFound, http.StatusGone:
		return ErrNotFound
	case http.StatusConflict:
		return ErrBusy
	case http.StatusTooManyRequests:
		wait := defaultWait
		if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil {
			wait = n
		}
		return &contract.ErrRateLimited{RetryAfter: min(max(wait, 1), contract.MaxRetryAfter)}
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return ErrRedirect
	}
	if status >= 500 {
		return ErrUnavailable
	}
	return ErrInvalid
}

// Describe is a fixed sentence for an error of this package.
func Describe(err error) string {
	for _, e := range []error{ErrBadAddress, ErrBadCredential, ErrUnauthorized, ErrForbidden, ErrNotFound, ErrInvalid, ErrBusy, ErrRedirect, ErrCertificate, ErrPin, ErrUnreadable, ErrTooLarge} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return ErrUnavailable.Error()
}
