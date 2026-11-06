// SPDX-License-Identifier: MIT
package mailtest

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// Sent is one message the fake accepted.
type Sent struct {
	User string
	From string
	To   []string
	Data []byte
}

// SMTP is a fake SMTP submission server with AUTH PLAIN and LOGIN.
type SMTP struct {
	Port int
	Host string

	srv  *smtp.Server
	ln   net.Listener
	mode Mode

	mu        sync.Mutex
	users     map[string]string
	sent      []Sent
	log       bytes.Buffer
	rejectTo  map[string]bool
	onlyLogin bool
	dropData  bool
	failData  *smtp.SMTPError
}

// NewSMTP starts a server. ca may be nil for ModePlain.
func NewSMTP(ca *CA, mode Mode) *SMTP {
	s := &SMTP{Host: "127.0.0.1", mode: mode, users: map[string]string{}, rejectTo: map[string]bool{}}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s.Port = raw.Addr().(*net.TCPAddr).Port
	s.srv = smtp.NewServer(s)
	s.srv.Domain = "mail.test"
	s.srv.AllowInsecureAuth = mode == ModePlain
	s.srv.MaxRecipients = 100
	s.srv.Debug = &smtpLog{s: s}
	if mode != ModePlain {
		s.srv.TLSConfig = ca.Config()
	}
	s.ln = raw
	if mode == ModeTLS {
		s.ln = ca.Wrap(raw)
	}
	go func() { _ = s.srv.Serve(s.ln) }()
	return s
}

// Close stops the server.
func (s *SMTP) Close() { _ = s.srv.Close(); _ = s.ln.Close() }

// AddUser adds a login.
func (s *SMTP) AddUser(name, password string) {
	s.mu.Lock()
	s.users[name] = password
	s.mu.Unlock()
}

// RejectRecipient makes the server refuse one recipient with 550.
func (s *SMTP) RejectRecipient(addr string) {
	s.mu.Lock()
	s.rejectTo[strings.ToLower(addr)] = true
	s.mu.Unlock()
}

// OnlyLogin makes the server offer AUTH LOGIN and not PLAIN.
func (s *SMTP) OnlyLogin() { s.mu.Lock(); s.onlyLogin = true; s.mu.Unlock() }

// DropAfterData makes the server read the message and then cut the connection
// without an answer, as a network failure does: the client cannot tell
// whether the message was taken.
func (s *SMTP) DropAfterData() { s.mu.Lock(); s.dropData = true; s.mu.Unlock() }

// FailData makes the server refuse the message with a code.
func (s *SMTP) FailData(code int, text string) {
	s.mu.Lock()
	s.failData = &smtp.SMTPError{Code: code, EnhancedCode: smtp.EnhancedCode{int(code / 100), 0, 0}, Message: text}
	s.mu.Unlock()
}

// Sent returns the accepted messages, oldest first.
func (s *SMTP) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// Transcript is the text the clients sent.
func (s *SMTP) Transcript() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.String()
}

type smtpLog struct{ s *SMTP }

func (l *smtpLog) Write(b []byte) (int, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	l.s.log.Write(b)
	return len(b), nil
}

// NewSession implements smtp.Backend.
func (s *SMTP) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &smtpSession{s: s, conn: c}, nil
}

type smtpSession struct {
	s    *SMTP
	conn *smtp.Conn
	user string
	from string
	to   []string
}

func (x *smtpSession) AuthMechanisms() []string {
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	if x.s.onlyLogin {
		return []string{sasl.Login}
	}
	return []string{sasl.Plain, sasl.Login}
}

func (x *smtpSession) Auth(mech string) (sasl.Server, error) {
	check := func(user, pass string) error {
		x.s.mu.Lock()
		defer x.s.mu.Unlock()
		if want, ok := x.s.users[user]; !ok || want != pass {
			return smtp.ErrAuthFailed
		}
		x.user = user
		return nil
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, user, pass string) error { return check(user, pass) }), nil
	case sasl.Login:
		return newLoginServer(check), nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

func (x *smtpSession) Mail(from string, _ *smtp.MailOptions) error {
	if x.user == "" {
		return smtp.ErrAuthRequired
	}
	x.from = from
	return nil
}

func (x *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	if x.s.rejectTo[strings.ToLower(to)] {
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "no such user"}
	}
	x.to = append(x.to, to)
	return nil
}

func (x *smtpSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	if x.s.failData != nil {
		return x.s.failData
	}
	x.s.sent = append(x.s.sent, Sent{User: x.user, From: x.from, To: append([]string(nil), x.to...), Data: b})
	if x.s.dropData {
		_ = x.conn.Conn().Close()
		return errors.New("dropped")
	}
	return nil
}

func (x *smtpSession) Reset()        { x.from, x.to = "", nil }
func (x *smtpSession) Logout() error { return nil }

// loginServer is the server side of AUTH LOGIN, which go-sasl only has as a client.
type loginServer struct {
	check func(user, pass string) error
	user  string
	step  int
}

func newLoginServer(check func(user, pass string) error) sasl.Server {
	return &loginServer{check: check}
}

func (l *loginServer) Next(response []byte) ([]byte, bool, error) {
	switch l.step {
	case 0:
		l.step = 1
		if len(response) > 0 {
			l.user = string(response)
			l.step = 2
			return []byte("Password:"), false, nil
		}
		return []byte("Username:"), false, nil
	case 1:
		l.user = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	default:
		return nil, true, l.check(l.user, string(response))
	}
}
