// SPDX-License-Identifier: MIT

// Package sshtest is an SSH server that runs inside the test process. It
// listens on loopback, logs a person in with a password or a public key, and
// answers an exec request with the output a test chose. It runs no real
// command and touches no real host, so the tests need no network.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Streams are the ends of one command.
type Streams struct {
	Stdout, Stderr io.Writer
	// Done is closed when the client goes away.
	Done <-chan struct{}
}

// Handler answers a command and returns its exit status.
type Handler func(command string, s Streams) int

// Options of a server. A zero value accepts nobody.
type Options struct {
	User     string
	Password string
	// Key, when set, is the one public key that may log in as User.
	Key ssh.PublicKey
	// Handler answers the commands. The default answers uname and id.
	Handler Handler
}

// Server is a running test server.
type Server struct {
	ln   net.Listener
	opts Options

	mu       sync.Mutex
	hostKey  ssh.Signer
	conns    []net.Conn
	commands []string
	requests []string
	logins   int
	refused  int
	wg       sync.WaitGroup
}

// New starts a server with a fresh ed25519 host key. Call Close when done.
func New(opts Options) *Server {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s := &Server{ln: ln, opts: opts, hostKey: NewSigner()}
	if s.opts.Handler == nil {
		s.opts.Handler = s.defaultHandler
	}
	s.wg.Add(1)
	go s.accept()
	return s
}

// Close stops the server.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

// Host and Port are the loopback address of the server.
func (s *Server) Host() string { return "127.0.0.1" }
func (s *Server) Port() string {
	_, p, _ := net.SplitHostPort(s.ln.Addr().String())
	return p
}

// Address is "127.0.0.1:port".
func (s *Server) Address() string { return s.ln.Addr().String() }

// HostKey returns the public key the server presents now.
func (s *Server) HostKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKey.PublicKey()
}

// RotateHostKey makes the server present a new key from now on.
func (s *Server) RotateHostKey() {
	s.mu.Lock()
	s.hostKey = NewSigner()
	s.mu.Unlock()
}

// DropConnections closes every client connection without a goodbye, as a
// network failure would.
func (s *Server) DropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

// Commands lists the exec requests the server got.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Requests lists the names of every channel request the server got, such as
// "exec", "pty-req", "env" or "shell".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Logins counts the clients that logged in.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// Refused counts the logins that failed.
func (s *Server) Refused() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused
}

func (s *Server) defaultHandler(command string, st Streams) int {
	switch command {
	case "uname -a":
		_, _ = io.WriteString(st.Stdout, "Linux testhost 6.1.0 #1 SMP x86_64 GNU/Linux\n")
	case "id -un":
		_, _ = io.WriteString(st.Stdout, s.opts.User+"\n")
	default:
		_, _ = io.WriteString(st.Stdout, "ran: "+command+"\n")
	}
	return 0
}

func (s *Server) config() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{}
	if s.opts.Password != "" {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == s.opts.User && string(pw) == s.opts.Password {
				return nil, nil
			}
			s.fail()
			return nil, io.EOF
		}
	}
	if s.opts.Key != nil {
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == s.opts.User && string(k.Marshal()) == string(s.opts.Key.Marshal()) {
				return nil, nil
			}
			s.fail()
			return nil, io.EOF
		}
	}
	s.mu.Lock()
	cfg.AddHostKey(s.hostKey)
	s.mu.Unlock()
	return cfg
}

func (s *Server) fail() {
	s.mu.Lock()
	s.refused++
	s.mu.Unlock()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(nc)
		}()
	}
}

func (s *Server) serve(nc net.Conn) {
	defer func() { _ = nc.Close() }()
	s.mu.Lock()
	s.conns = append(s.conns, nc)
	s.mu.Unlock()
	conn, chans, reqs, err := ssh.NewServerConn(nc, s.config())
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			return
		}
		go s.session(conn, ch, creqs)
	}
}

func (s *Server) session(conn *ssh.ServerConn, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	gone := make(chan struct{})
	go func() { _ = conn.Wait(); close(gone) }()
	for r := range reqs {
		s.mu.Lock()
		s.requests = append(s.requests, r.Type)
		s.mu.Unlock()
		if r.Type != "exec" {
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
			continue
		}
		var p struct{ Command string }
		if ssh.Unmarshal(r.Payload, &p) != nil {
			_ = r.Reply(false, nil)
			return
		}
		s.mu.Lock()
		s.commands = append(s.commands, p.Command)
		s.mu.Unlock()
		_ = r.Reply(true, nil)
		code := s.opts.Handler(p.Command, Streams{Stdout: ch, Stderr: ch.Stderr(), Done: gone})
		var status [4]byte
		binary.BigEndian.PutUint32(status[:], uint32(code))
		_, _ = ch.SendRequest("exit-status", false, status[:])
		return
	}
}

// NewSigner makes a fresh ed25519 key.
func NewSigner() ssh.Signer {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		panic(err)
	}
	return sg
}

// NewClientKey makes a client key and returns its public half and its
// private half as an OpenSSH PEM.
func NewClientKey() (ssh.PublicKey, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		panic(err)
	}
	sp, _ := ssh.NewPublicKey(pub)
	return sp, string(pem.EncodeToMemory(blk))
}

// NewClientKeyPKCS8 is NewClientKey with the private half as a PKCS#8 PEM.
func NewClientKeyPKCS8() (ssh.PublicKey, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		panic(err)
	}
	sp, _ := ssh.NewPublicKey(pub)
	return sp, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// NewEncryptedClientKey makes an OpenSSH key protected by a passphrase.
func NewEncryptedClientKey(passphrase string) string {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	blk, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(blk))
}
