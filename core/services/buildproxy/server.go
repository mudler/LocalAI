package buildproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type certificateAuthority struct {
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

type Server struct {
	server   *http.Server
	listener net.Listener
	handler  http.Handler
	recorder *Recorder
	ca       *certificateAuthority
	caPath   string
	wg       sync.WaitGroup
}

func NewServer(address, caDir string, handler http.Handler, recorder *Recorder) (*Server, error) {
	ca, caPath, err := createCA(caDir)
	if err != nil {
		return nil, err
	}
	s := &Server{handler: handler, recorder: recorder, ca: ca, caPath: caPath}
	s.server = &http.Server{Addr: address, Handler: http.HandlerFunc(s.serveHTTP), ReadHeaderTimeout: 30 * time.Second}
	return s, nil
}

func (s *Server) CAPath() string { return s.caPath }
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.recorder.Record(Event{Method: "PROXY", Attempts: 1, Error: err.Error()})
		}
	}()
	return nil
}
func (s *Server) Addr() string { return s.listener.Addr().String() }
func (s *Server) Stop(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	s.wg.Wait()
	return err
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		// Some minimal clients (notably BusyBox wget) send an absolute HTTPS
		// request to an HTTP forward proxy instead of opening CONNECT. The
		// resource hop remains TLS and is handled by the same verified upstream
		// transport; only absolute http:// resource URLs are forbidden.
		if r.URL != nil && r.URL.IsAbs() && r.URL.Scheme == "https" {
			// BusyBox closes its request side after writing the absolute-form
			// request. Detach that connection cancellation while the proxy
			// completes and verifies the upstream response.
			s.handler.ServeHTTP(w, r.Clone(context.WithoutCancel(r.Context())))
			return
		}
		s.recorder.Record(Event{Host: hostname(r.Host), Method: r.Method, Path: r.URL.EscapedPath(), Attempts: 1, Error: "plain HTTP is forbidden"})
		http.Error(w, "build proxy: plain HTTP is forbidden", http.StatusUpgradeRequired)
		return
	}
	s.intercept(w, r)
}

func (s *Server) intercept(w http.ResponseWriter, r *http.Request) {
	host := hostname(r.Host)
	leaf, err := s.ca.leaf(host)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unavailable", 500)
		return
	}
	conn, _, err := h.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*leaf}, NextProtos: []string{"http/1.1"}})
	if err = tlsConn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return
	}
	if err = tlsConn.Handshake(); err != nil {
		s.recorder.Record(Event{Host: host, Method: "CONNECT", Attempts: 1, Error: err.Error()})
		return
	}
	_ = tlsConn.SetDeadline(time.Time{})
	ln := &singleListener{conn: tlsConn, done: make(chan struct{})}
	inner := &http.Server{Handler: http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		req.URL.Scheme = "https"
		req.URL.Host = r.Host
		s.handler.ServeHTTP(rw, req)
	})}
	_ = inner.Serve(ln)
}

type singleListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *singleListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = &signalConn{Conn: l.conn, done: l.done} })
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}
func (l *singleListener) Close() error   { return nil }
func (l *singleListener) Addr() net.Addr { return l.conn.LocalAddr() }

type signalConn struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *signalConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.done) })
	return err
}

func createCA(dir string) (*certificateAuthority, string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "LocalAI CI Build Proxy"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, t, t, &key.PublicKey, key)
	if err != nil {
		return nil, "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "ca.crt")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return nil, "", err
	}
	return &certificateAuthority{cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, path, nil
}
func (c *certificateAuthority) leaf(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v := c.leaves[host]; v != nil {
		return v, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		t.IPAddresses = []net.IP{ip}
	} else {
		t.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, t, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		return nil, err
	}
	c.leaves[host] = &pair
	return &pair, nil
}

func ParseListenAddress(address string) (string, error) {
	if strings.TrimSpace(address) == "" {
		return "", fmt.Errorf("listen address is empty")
	}
	return address, nil
}
