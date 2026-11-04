// SPDX-License-Identifier: MIT
package mailtest

import (
	"bytes"
	"net"
	"regexp"
	"strings"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// Mode is how a fake server protects its connections.
type Mode string

const (
	// ModeTLS is implicit TLS.
	ModeTLS Mode = "tls"
	// ModeStartTLS offers STARTTLS and refuses to log in before it.
	ModeStartTLS Mode = "starttls"
	// ModePlain speaks in clear text and lets clients log in that way.
	ModePlain Mode = "none"
)

// IMAP is a fake IMAP server over the in-memory server of go-imap. It marks
// a message \Seen when a client fetches a body without PEEK, as a real server
// does, so a test sees a client that reads without that care.
type IMAP struct {
	// Port is the port on 127.0.0.1.
	Port int
	// Host is the address to give the add-on.
	Host string

	mem  *imapmemserver.Server
	srv  *imapserver.Server
	ln   net.Listener
	mode Mode

	mu      sync.Mutex
	users   map[string]*imapmemserver.User
	log     bytes.Buffer
	hang    bool
	held    []net.Conn
	appends []Append
	boxes   map[string][]box
}

type box struct {
	name    string
	special []imap.MailboxAttr
}

// Append is one message a client appended.
type Append struct {
	User, Mailbox string
	Flags         []string
	Raw           []byte
}

// NewIMAP starts a server. ca may be nil for ModePlain.
func NewIMAP(ca *CA, mode Mode) *IMAP {
	s := &IMAP{Host: "127.0.0.1", mem: imapmemserver.New(), mode: mode, users: map[string]*imapmemserver.User{}, boxes: map[string][]box{}}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s.Port = raw.Addr().(*net.TCPAddr).Port
	opts := &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &session{Session: s.mem.NewSession(), s: s}, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapListExtended: {}, imap.CapSpecialUse: {}, imap.CapCreateSpecialUse: {}, imap.CapChildren: {}},
		InsecureAuth: mode == ModePlain,
		DebugWriter:  &logWriter{s: s},
	}
	if mode != ModePlain {
		opts.TLSConfig = ca.Config()
	}
	s.srv = imapserver.New(opts)
	s.ln = &gate{Listener: raw, s: s}
	if mode == ModeTLS {
		s.ln = ca.Wrap(s.ln)
	}
	go func() { _ = s.srv.Serve(s.ln) }()
	return s
}

// Close stops the server.
func (s *IMAP) Close() {
	_ = s.srv.Close()
	_ = s.ln.Close()
	s.mu.Lock()
	for _, c := range s.held {
		_ = c.Close()
	}
	s.mu.Unlock()
}

// AddUser adds a user and makes its INBOX.
func (s *IMAP) AddUser(name, password string) {
	u := imapmemserver.NewUser(name, password)
	if err := u.Create("INBOX", nil); err != nil {
		panic(err)
	}
	s.mem.AddUser(u)
	s.mu.Lock()
	s.users[name] = u
	s.boxes[name] = []box{{name: "INBOX"}}
	s.mu.Unlock()
}

// Mailbox creates a mailbox with the given special-use attributes, such as
// `\Drafts`.
func (s *IMAP) Mailbox(user, name string, special ...string) {
	var attrs []imap.MailboxAttr
	for _, a := range special {
		attrs = append(attrs, imap.MailboxAttr(a))
	}
	if err := s.user(user).Create(name, &imap.CreateOptions{SpecialUse: attrs}); err != nil {
		panic(err)
	}
	s.mu.Lock()
	s.boxes[user] = append(s.boxes[user], box{name: name, special: attrs})
	s.mu.Unlock()
}

func (s *IMAP) user(name string) *imapmemserver.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[name]
	if u == nil {
		panic("mailtest: no user " + name)
	}
	return u
}

// Deliver puts a raw message (CRLF line ends) in a mailbox and returns its UID.
func (s *IMAP) Deliver(user, mailbox string, raw string, flags ...string) uint32 {
	var fl []imap.Flag
	for _, f := range flags {
		fl = append(fl, imap.Flag(f))
	}
	d, err := s.user(user).Append(mailbox, &literal{Reader: strings.NewReader(raw), n: int64(len(raw))}, &imap.AppendOptions{Flags: fl})
	if err != nil {
		panic(err)
	}
	return uint32(d.UID)
}

type literal struct {
	*strings.Reader
	n int64
}

func (l *literal) Size() int64 { return l.n }

// UnseenCount is how many messages of a mailbox do not carry \Seen.
func (s *IMAP) UnseenCount(user, mailbox string) int {
	d, err := s.user(user).Status(mailbox, &imap.StatusOptions{NumUnseen: true})
	if err != nil {
		panic(err)
	}
	return int(*d.NumUnseen)
}

// Appended returns what clients appended, oldest first.
func (s *IMAP) Appended() []Append {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Append(nil), s.appends...)
}

// Transcript is everything that crossed the connection, both ways, as text.
func (s *IMAP) Transcript() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.ReplaceAll(s.log.String(), "\r\n", "\n")
}

var tagged = regexp.MustCompile(`^T\d+ (\S+)`)

// Commands returns the commands the clients sent, one tagged line each. Lines
// the server sent, and the text of literals, are left out.
func (s *IMAP) Commands() []string {
	var out []string
	for _, l := range strings.Split(s.Transcript(), "\n") {
		if m := tagged.FindStringSubmatch(l); m != nil && m[1] != "OK" && m[1] != "NO" && m[1] != "BAD" {
			out = append(out, l)
		}
	}
	return out
}

// Hang makes the server accept connections and never answer.
func (s *IMAP) Hang(on bool) {
	s.mu.Lock()
	s.hang = on
	s.mu.Unlock()
}

// session wraps the in-memory session. It sets \Seen on a fetch of a body
// without PEEK and keeps what is appended.
type session struct {
	imapserver.Session
	s    *IMAP
	user string
}

func (x *session) Login(user, pass string) error {
	if err := x.Session.Login(user, pass); err != nil {
		return err
	}
	x.user = user
	return nil
}

// List answers from the fake's own record, because the in-memory server keeps
// no special-use attributes.
func (x *session) List(w *imapserver.ListWriter, ref string, patterns []string, o *imap.ListOptions) error {
	x.s.mu.Lock()
	boxes := append([]box(nil), x.s.boxes[x.user]...)
	x.s.mu.Unlock()
	for _, b := range boxes {
		match := false
		for _, p := range patterns {
			match = match || imapserver.MatchList(b.name, '/', ref, p)
		}
		if !match {
			continue
		}
		d := &imap.ListData{Mailbox: b.name, Delim: '/'}
		if o != nil && (o.ReturnSpecialUse || o.SelectSpecialUse) {
			d.Attrs = b.special
		}
		if err := w.WriteList(d); err != nil {
			return err
		}
	}
	return nil
}

func (x *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, o *imap.FetchOptions) error {
	if err := x.Session.Fetch(w, numSet, o); err != nil {
		return err
	}
	for _, bs := range o.BodySection {
		if !bs.Peek {
			return x.Session.Store(nil, numSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil)
		}
	}
	return nil
}

func (x *session) Append(mailbox string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	x.s.mu.Lock()
	var flags []string
	if o != nil {
		for _, f := range o.Flags {
			flags = append(flags, string(f))
		}
	}
	x.s.appends = append(x.s.appends, Append{User: x.user, Mailbox: mailbox, Flags: flags, Raw: buf.Bytes()})
	x.s.mu.Unlock()
	return x.Session.Append(mailbox, &literal{Reader: strings.NewReader(buf.String()), n: int64(buf.Len())}, o)
}

type logWriter struct{ s *IMAP }

func (l *logWriter) Write(b []byte) (int, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	l.s.log.Write(b)
	return len(b), nil
}

// gate holds connections while the server hangs.
type gate struct {
	net.Listener
	s *IMAP
}

func (g *gate) Accept() (net.Conn, error) {
	for {
		c, err := g.Listener.Accept()
		if err != nil {
			return nil, err
		}
		g.s.mu.Lock()
		if g.s.hang {
			g.s.held = append(g.s.held, c)
			g.s.mu.Unlock()
			continue
		}
		g.s.mu.Unlock()
		return c, nil
	}
}
