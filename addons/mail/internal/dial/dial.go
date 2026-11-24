// SPDX-License-Identifier: MIT

// Package dial opens the TCP connections of the Mail add-on. IMAP and SMTP are
// not HTTP, so the egress check of the sandbox proxy does not cover them; this
// add-on is trusted code with its own network access, and it applies the same
// guard that web_fetch and the sandbox proxy use (pkg/web/safehttp): a host the
// administrator or a person typed is checked for its shape, its port and every
// address it resolves to before one byte is sent, and the address that was
// checked is the address that is dialed. Private, loopback, link-local,
// metadata and the server's own addresses are refused.
package dial

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// Errors of a connection. None of them holds a host name or an address.
var (
	// ErrBlocked means the host is not one the add-on may reach: it is inside
	// a private network, is the server itself, or has no valid form.
	ErrBlocked = errors.New("the mail server address is not allowed (private and local addresses are refused)")
	// ErrConnect means the server could not be reached.
	ErrConnect = errors.New("could not connect to the mail server")
	// ErrTimeout means the server did not answer in time.
	ErrTimeout = errors.New("the mail server did not answer in time")
	// ErrTLS means the TLS handshake failed or the certificate was refused.
	ErrTLS = errors.New("the mail server's TLS certificate was not accepted")
)

// Options configure a Dialer. The zero value is the production policy.
type Options struct {
	// Ports are the ports a host may name. Empty: the IMAP and SMTP ports of
	// package account.
	Ports []int
	// RootCAs replaces the system roots. Only a test sets it.
	RootCAs *x509.CertPool
	// TreatAsPublic lets a test reach a server on loopback; production code
	// leaves it empty.
	TreatAsPublic []netip.Addr
	// Timeout bounds the connection and the TLS handshake. Zero: 10 s.
	Timeout time.Duration
}

// Dialer opens guarded connections. It is safe for concurrent use.
type Dialer struct {
	guard   *safehttp.Guard
	roots   *x509.CertPool
	timeout time.Duration
}

// New returns a Dialer.
func New(o Options) *Dialer {
	ports := o.Ports
	if len(ports) == 0 {
		ports = append(append([]int{}, account.IMAPPorts...), account.SMTPPorts...)
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	g := safehttp.NewGuard(safehttp.GuardPolicy{
		Ports:         ports,
		DenyAddrs:     safehttp.SelfAddrs(nil),
		TreatAsPublic: o.TreatAsPublic,
	}, nil)
	return &Dialer{guard: g, roots: o.RootCAs, timeout: o.Timeout}
}

// TLSConfig is the client configuration for a host: the certificate is
// verified against the host name, TLS 1.2 at least. There is no option to skip
// the verification.
func (d *Dialer) TLSConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: d.roots}
}

// Open connects to the endpoint. With implicit TLS the handshake is done and
// the connection is the encrypted one. With STARTTLS or no security the caller
// gets the plain connection and upgrades it itself (TLSConfig).
func (d *Dialer) Open(ctx context.Context, ep account.Endpoint) (net.Conn, error) {
	host, err := d.guard.CheckHost(ep.Host, ep.Port)
	if err != nil {
		return nil, ErrBlocked
	}
	dctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	raw, err := d.guard.DialContext(dctx, "tcp", net.JoinHostPort(host, itoa(ep.Port)))
	if err != nil {
		return nil, classify(ctx, err)
	}
	if ep.Security != account.TLS {
		return raw, nil
	}
	tc := tls.Client(raw, d.TLSConfig(host))
	if err := tc.HandshakeContext(dctx); err != nil {
		_ = raw.Close()
		return nil, classify(ctx, err)
	}
	return tc, nil
}

func itoa(n int) string {
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// classify maps a failure to one of the errors above.
func classify(ctx context.Context, err error) error {
	var (
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		verify   *tls.CertificateVerificationError
		header   tls.RecordHeaderError
		ne       net.Error
	)
	switch {
	case errors.Is(err, safehttp.ErrBlockedAddress), errors.Is(err, safehttp.ErrURL), errors.Is(err, safehttp.ErrPort):
		return ErrBlocked
	case errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &verify), errors.As(err, &header):
		return ErrTLS
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), (errors.As(err, &ne) && ne.Timeout()):
		return ErrTimeout
	case ctx.Err() != nil:
		return ctx.Err()
	}
	if isTLSAlert(err) {
		return ErrTLS
	}
	return ErrConnect
}

// isTLSAlert reports a handshake the peer ended with an alert.
func isTLSAlert(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "remote error"
}
