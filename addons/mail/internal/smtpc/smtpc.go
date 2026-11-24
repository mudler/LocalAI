// SPDX-License-Identifier: MIT

// Package smtpc is the SMTP side of the Mail add-on: it logs in with AUTH
// PLAIN, or AUTH LOGIN when that is all the server offers, and submits one
// message. The connection is protected as the account says (implicit TLS or
// STARTTLS) and a failed STARTTLS never falls back to clear text.
package smtpc

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
)

// Errors. A definite one proves the server took no message. ErrUnknown does not.
var (
	ErrAuth      = errors.New("the mail server refused the username or password for sending (some providers need an app password)")
	ErrNoAuth    = errors.New("the mail server offers no login method this add-on supports")
	ErrSTARTTLS  = errors.New("the mail server does not offer STARTTLS, so the connection was not used")
	ErrSender    = errors.New("the mail server refused the sender address")
	ErrRecipient = errors.New("the mail server refused a recipient address")
	ErrRejected  = errors.New("the mail server refused the message")
	// ErrUnknown means the connection failed after the message was sent. The
	// server may or may not have taken it.
	ErrUnknown = errors.New("the connection failed while the message was sent; it is not known whether it was sent")
	ErrServer  = errors.New("the mail server answered with an error")
)

// Definite tells that the error proves nothing was submitted.
func Definite(err error) bool { return err != nil && !errors.Is(err, ErrUnknown) }

type client struct {
	c    *smtp.Client
	conn net.Conn
	stop func()
}

func (c *client) close() {
	_ = c.c.Close()
	_ = c.conn.Close()
	c.stop()
}

func open(ctx context.Context, d *dial.Dialer, a account.Account) (*client, error) {
	conn, err := d.Open(ctx, a.SMTP)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	stop := func() { close(done) }
	var c *smtp.Client
	if a.SMTP.Security == account.StartTLS {
		c, err = smtp.NewClientStartTLS(conn, d.TLSConfig(a.SMTP.Host))
		if err != nil {
			stop()
			_ = conn.Close()
			var se *smtp.SMTPError
			if errors.As(err, &se) || isNoTLS(err) {
				return nil, ErrSTARTTLS
			}
			return nil, mapNet(ctx, err)
		}
	} else {
		c = smtp.NewClient(conn)
		if err := c.Hello("localhost"); err != nil {
			stop()
			_ = conn.Close()
			return nil, mapNet(ctx, err)
		}
	}
	cl := &client{c: c, conn: conn, stop: stop}
	if err := cl.auth(ctx, a); err != nil {
		cl.close()
		return nil, err
	}
	return cl, nil
}

func isNoTLS(err error) bool {
	return err != nil && (errors.Is(err, smtp.ErrAuthUnsupported) || strings.Contains(err.Error(), "STARTTLS"))
}

func (c *client) auth(ctx context.Context, a account.Account) error {
	var cl sasl.Client
	switch {
	case c.c.SupportsAuth(sasl.Plain):
		cl = sasl.NewPlainClient("", a.Username, a.Password)
	case c.c.SupportsAuth(sasl.Login):
		cl = sasl.NewLoginClient(a.Username, a.Password)
	default:
		return ErrNoAuth
	}
	if err := c.c.Auth(cl); err != nil {
		var se *smtp.SMTPError
		if errors.As(err, &se) {
			return ErrAuth
		}
		return mapNet(ctx, err)
	}
	return nil
}

func mapNet(ctx context.Context, err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, dial.ErrTLS):
		return dial.ErrTLS
	case ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled):
		return dial.ErrTimeout
	case errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return dial.ErrTimeout
	}
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		return ErrServer
	}
	return dial.ErrConnect
}

// Check connects and logs in, and sends nothing. The settings page uses it.
func Check(ctx context.Context, d *dial.Dialer, a account.Account) error {
	c, err := open(ctx, d, a)
	if err != nil {
		return err
	}
	c.close()
	return nil
}

// Send submits raw (a complete message, CRLF line ends) from the envelope
// sender to the recipients.
func Send(ctx context.Context, d *dial.Dialer, a account.Account, from string, rcpts []string, raw []byte) error {
	c, err := open(ctx, d, a)
	if err != nil {
		return err
	}
	defer c.close()
	if err := c.c.Mail(from, nil); err != nil {
		return stage(ctx, err, ErrSender)
	}
	for _, r := range rcpts {
		if err := c.c.Rcpt(r, nil); err != nil {
			return stage(ctx, err, ErrRecipient)
		}
	}
	w, err := c.c.Data()
	if err != nil {
		return stage(ctx, err, ErrRejected)
	}
	if _, err := w.Write(raw); err != nil {
		return ErrUnknown
	}
	if err := w.Close(); err != nil {
		var se *smtp.SMTPError
		if errors.As(err, &se) {
			return ErrRejected
		}
		return ErrUnknown
	}
	_ = c.c.Quit()
	return nil
}

// stage maps an error before the message body was sent: a refusal by the
// server is the given sentinel, a network failure is a connection error. In
// both cases the server holds no message.
func stage(ctx context.Context, err error, refusal error) error {
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		return refusal
	}
	return mapNet(ctx, err)
}
