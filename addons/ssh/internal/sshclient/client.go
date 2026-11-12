// SPDX-License-Identifier: MIT

// Package sshclient runs one command on one machine over SSH. It dials only
// the address of the connection and trusts only the host key pinned in that
// address. The host key is checked during the key exchange, before any
// credential is sent, so a machine that presents another key never sees the
// password.
//
// A session has no terminal, no standard input and no environment variables.
// There is no shell, file transfer, port forwarding, agent forwarding or jump
// host. The credential never appears in an error built here.
package sshclient

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/ssh/internal/credential"
	"golang.org/x/crypto/ssh"
)

// Limits of a command.
const (
	// MaxOutput bounds each of standard output and standard error in a result.
	MaxOutput = 32 << 10
	// DialTimeout bounds the connection and the login.
	DialTimeout = 8 * time.Second
	// keep is how much of a stream is held before it is scrubbed. It exceeds
	// MaxOutput by the longest secret, so a secret that crosses the cut is
	// still found whole.
	keep = MaxOutput + credential.MaxLength
)

// Errors of a connection. Each one has its own plain message. None of them
// holds the credential or text the machine sent.
var (
	ErrUnreachable = errors.New("the host could not be reached (connection refused, timed out or no route); nothing was run")
	ErrAuth        = errors.New("the host refused the login (wrong user, password or key); nothing was run")
	ErrNoPin       = errors.New("no host key is pinned for this connection; the person must check and trust the host key on the SSH page first; nothing was run")
	ErrHandshake   = errors.New("the SSH handshake failed (the host may not run SSH on this port); nothing was run")
	ErrNoSession   = errors.New("the host refused to open a session or to run a command; nothing was run")
	ErrLost        = errors.New("the connection was lost after the command started; whether it finished is unknown")
)

// HostKeyChangedError means the host presents another key than the pinned one.
type HostKeyChangedError struct{ Pinned, Presented string }

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("the host key changed: the pinned key is %s and the host now presents %s. This can mean the machine was reinstalled, or that someone is intercepting the connection. Nothing was sent. Only the person can trust a new key, by editing the connection on the SSH page", e.Pinned, e.Presented)
}

// errScanned stops the handshake once the host key is known.
var errScanned = errors.New("host key read")

// Conn is an open, logged-in connection.
type Conn struct {
	client *ssh.Client
	nc     net.Conn
	stop   func() bool
}

// HostKey is a key a host presented.
type HostKey struct {
	Type        string
	Pin         string
	Fingerprint string
}

// Connect dials the target, checks its host key against the pin and logs in.
func Connect(ctx context.Context, t Target, cred *credential.Credential) (*Conn, error) {
	if t.Pin == "" {
		return nil, ErrNoPin
	}
	var changed *HostKeyChangedError
	cfg := &ssh.ClientConfig{
		User: t.User,
		Auth: cred.Auth(),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got := PinHex(key)
			if subtle.ConstantTimeCompare([]byte(got), []byte(t.Pin)) != 1 {
				changed = &HostKeyChangedError{Pinned: FingerprintOfPin(t.Pin), Presented: FingerprintOfPin(got)}
				return changed
			}
			return nil
		},
		Timeout: DialTimeout,
	}
	nc, stop, err := open(ctx, t)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(nc, t.HostPort(), cfg)
	_ = nc.SetDeadline(time.Time{})
	if err != nil {
		stop()
		_ = nc.Close()
		switch {
		case changed != nil:
			return nil, changed
		case ctx.Err() != nil:
			return nil, ErrUnreachable
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, ErrAuth
		}
		return nil, ErrHandshake
	}
	return &Conn{client: ssh.NewClient(c, chans, reqs), nc: nc, stop: stop}, nil
}

// ScanHostKey reads the host key a machine presents and stops. It sends no
// credential: the handshake ends in the host key check.
func ScanHostKey(ctx context.Context, t Target) (HostKey, error) {
	var got HostKey
	cfg := &ssh.ClientConfig{
		User: t.User,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = HostKey{Type: key.Type(), Pin: PinHex(key), Fingerprint: ssh.FingerprintSHA256(key)}
			return errScanned
		},
		Timeout: DialTimeout,
	}
	nc, stop, err := open(ctx, t)
	if err != nil {
		return HostKey{}, err
	}
	defer stop()
	defer func() { _ = nc.Close() }()
	_, _, _, err = ssh.NewClientConn(nc, t.HostPort(), cfg)
	if got.Pin != "" {
		return got, nil
	}
	if ctx.Err() != nil || err == nil {
		return HostKey{}, ErrUnreachable
	}
	return HostKey{}, ErrHandshake
}

// open dials the target and arms the deadline of the handshake. The returned
// stop function ends the watch on the context.
func open(ctx context.Context, t Target) (net.Conn, func() bool, error) {
	d := net.Dialer{Timeout: DialTimeout}
	nc, err := d.DialContext(ctx, "tcp", t.HostPort())
	if err != nil {
		return nil, nil, ErrUnreachable
	}
	_ = nc.SetDeadline(time.Now().Add(DialTimeout))
	stop := context.AfterFunc(ctx, func() { _ = nc.Close() })
	return nc, stop, nil
}

// Close ends the connection.
func (c *Conn) Close() {
	c.stop()
	_ = c.client.Close()
	_ = c.nc.Close()
}

// Result is what a command did.
type Result struct {
	// ExitCode is -1 when the command ended without one (a signal or a timeout).
	ExitCode        int
	Signal          string
	Stdout, Stderr  string
	StdoutTruncated bool
	StderrTruncated bool
	TimedOut        bool
	Duration        time.Duration
}

// Run starts one command and waits for it. onStart runs just before the
// command is sent; if it fails, the command is not sent. scrub removes
// secrets from the output. When the timeout passes, the connection is closed
// and the result holds the output so far with TimedOut set: the command may
// keep running on the host.
func (c *Conn) Run(ctx context.Context, command string, timeout time.Duration, onStart func() error, scrub func(string) string) (Result, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return Result{}, ErrNoSession
	}
	defer func() { _ = sess.Close() }()
	out, errw := &capture{}, &capture{}
	sess.Stdout, sess.Stderr = out, errw
	if onStart != nil {
		if err := onStart(); err != nil {
			return Result{}, err
		}
	}
	started := time.Now()
	if err := sess.Start(command); err != nil {
		return Result{}, ErrNoSession
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	res := Result{ExitCode: -1}
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		res.TimedOut = true
		_ = sess.Signal(ssh.SIGKILL)
		c.Close()
		<-done
	case <-ctx.Done():
		res.TimedOut = true
		c.Close()
		<-done
	}
	res.Duration = time.Since(started)
	if !res.TimedOut {
		var exit *ssh.ExitError
		var missing *ssh.ExitMissingError
		switch {
		case waitErr == nil:
			res.ExitCode = 0
		case errors.As(waitErr, &exit):
			res.ExitCode, res.Signal = exit.ExitStatus(), exit.Signal()
			if res.Signal != "" {
				res.ExitCode = -1
			}
		case errors.As(waitErr, &missing), errors.Is(waitErr, io.EOF):
			return Result{}, ErrLost
		default:
			return Result{}, ErrLost
		}
	}
	res.Stdout, res.StdoutTruncated = out.text(scrub)
	res.Stderr, res.StderrTruncated = errw.text(scrub)
	return res, nil
}

// capture keeps the start of a stream and counts the rest.
type capture struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	extra bool
}

func (w *capture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := keep - w.buf.Len(); room > 0 {
		n := min(room, len(p))
		w.buf.Write(p[:n])
		if n < len(p) {
			w.extra = true
		}
	} else if len(p) > 0 {
		w.extra = true
	}
	return len(p), nil
}

// text returns the stream as valid UTF-8, scrubbed and cut to MaxOutput, and
// whether anything was cut.
func (w *capture) text(scrub func(string) string) (string, bool) {
	w.mu.Lock()
	raw := append([]byte(nil), w.buf.Bytes()...)
	cut := w.extra
	w.mu.Unlock()
	s := strings.ToValidUTF8(string(raw), "�")
	if scrub != nil {
		s = scrub(s)
	}
	if len(s) > MaxOutput {
		s = s[:MaxOutput]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		cut = true
	}
	return s, cut
}
