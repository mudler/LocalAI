package mitm

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/xlog"
	"golang.org/x/net/http2"
)

// Server is an HTTPS forward proxy that MITMs traffic for hosts
// in its intercept allowlist; non-allowlisted hosts get a plain
// TCP CONNECT tunnel.
type Server struct {
	addr            string
	ca              *CA
	interceptHosts  map[string]bool
	handler         InterceptHandler
	connectTimeout  time.Duration
	dialTimeout     time.Duration
	upstreamTLS     *tls.Config
	events          pii.EventStore
	eventSeq        atomic.Uint64

	listener net.Listener
	srv      *http.Server

	wg       sync.WaitGroup
	stopOnce sync.Once
	stopped  chan struct{}
}

// InterceptHandler runs after the proxy terminates TLS for an
// allowlisted host. It is responsible for forwarding the upstream
// response bytes back to w.
type InterceptHandler func(w http.ResponseWriter, r *http.Request, upstreamHost string)

type Config struct {
	Addr           string
	CA             *CA
	InterceptHosts []string
	Handler        InterceptHandler
	// EventStore optionally receives a proxy_connect event for every
	// CONNECT, recording the destination host and whether the proxy
	// intercepted or tunneled it. nil disables connect-event recording.
	EventStore pii.EventStore
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.CA == nil {
		return nil, errors.New("mitm: NewServer: CA is required")
	}
	if cfg.Handler == nil {
		return nil, errors.New("mitm: NewServer: Handler is required")
	}
	hosts := make(map[string]bool, len(cfg.InterceptHosts))
	for _, h := range cfg.InterceptHosts {
		hosts[strings.ToLower(strings.TrimSpace(h))] = true
	}
	return &Server{
		addr:           cfg.Addr,
		ca:             cfg.CA,
		interceptHosts: hosts,
		handler:        cfg.Handler,
		connectTimeout: 30 * time.Second,
		dialTimeout:    15 * time.Second,
		upstreamTLS:    &tls.Config{NextProtos: []string{"http/1.1"}},
		events:         cfg.EventStore,
		stopped:        make(chan struct{}),
	}, nil
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mitm: listen %q: %w", s.addr, err)
	}
	s.listener = ln
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 30 * time.Second,
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			xlog.Error("mitm: serve error", "error", err)
		}
	}()
	xlog.Info("mitm: listening", "addr", ln.Addr().String(), "intercept_hosts", len(s.interceptHosts))
	return nil
}

// Addr returns the bound listener address. Useful when Start was
// called with ":0" — the kernel picks a port and tests need to
// discover which.
func (s *Server) Addr() string {
	if s.listener == nil {
		return s.addr
	}
	return s.listener.Addr().String()
}

// Stop is idempotent.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		if s.srv != nil {
			_ = s.srv.Close()
		}
		s.wg.Wait()
	})
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "this proxy only supports HTTPS via CONNECT", http.StatusMethodNotAllowed)
		return
	}

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	host = strings.ToLower(host)

	intercept := s.shouldIntercept(host)
	s.recordConnectEvent(host, intercept)
	if !intercept {
		s.handleTunnel(w, r)
		return
	}
	s.handleIntercept(w, r, host)
}

// recordConnectEvent writes a proxy_connect audit row. Best-effort —
// store errors are logged at debug only so a failing recorder cannot
// break a CONNECT.
func (s *Server) recordConnectEvent(host string, intercepted bool) {
	if s.events == nil {
		return
	}
	flag := intercepted
	ev := pii.PIIEvent{
		ID:          fmt.Sprintf("proxy_connect_%d", s.eventSeq.Add(1)),
		Kind:        pii.KindProxyConnect,
		Host:        host,
		Intercepted: &flag,
		CreatedAt:   time.Now(),
	}
	if err := s.events.Record(context.Background(), ev); err != nil {
		xlog.Debug("mitm: failed to record proxy_connect event", "error", err, "host", host)
	}
}

// shouldIntercept reports whether host is in the allowlist. An
// empty allowlist tunnels everything.
func (s *Server) shouldIntercept(host string) bool {
	if len(s.interceptHosts) == 0 {
		return false
	}
	return s.interceptHosts[host]
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := net.DialTimeout("tcp", normalizeHostPort(r.Host), s.dialTimeout)
	if err != nil {
		http.Error(w, "mitm: tunnel dial: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "mitm: hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "mitm: hijack failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = clientConn.Close() }()

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

	pipe(clientConn, upstream)
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		_ = a.SetDeadline(time.Now())
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		_ = b.SetDeadline(time.Now())
		done <- struct{}{}
	}()
	<-done
}

func (s *Server) handleIntercept(w http.ResponseWriter, r *http.Request, host string) {
	leaf, err := s.ca.IssueLeaf(host)
	if err != nil {
		http.Error(w, "mitm: leaf issuance failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "mitm: hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "mitm: hijack failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = clientConn.Close() }()

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

	tlsConn := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		NextProtos:   []string{"h2", "http/1.1"},
	})
	defer func() { _ = tlsConn.Close() }()

	// Deadline applies to the handshake only; cleared before the
	// request loop so long-running streams don't get cut off. Fail
	// closed if SetDeadline errors — better than handshaking without
	// a deadline.
	if err := tlsConn.SetDeadline(time.Now().Add(s.connectTimeout)); err != nil {
		xlog.Debug("mitm: TLS handshake set-deadline failed", "host", host, "error", err)
		return
	}
	if err := tlsConn.Handshake(); err != nil {
		xlog.Debug("mitm: TLS handshake failed", "host", host, "error", err)
		return
	}
	_ = tlsConn.SetDeadline(time.Time{})

	handler := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		req.URL.Scheme = "https"
		if req.URL.Host == "" {
			req.URL.Host = req.Host
		}
		s.handler(rw, req, host)
	})

	switch tlsConn.ConnectionState().NegotiatedProtocol {
	case "h2":
		h2srv := &http2.Server{}
		h2srv.ServeConn(tlsConn, &http2.ServeConnOpts{
			Handler: handler,
			Context: r.Context(),
		})
	default:
		s.serveHTTP1(tlsConn, handler, host)
	}
}

func (s *Server) serveHTTP1(tlsConn *tls.Conn, handler http.Handler, host string) {
	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				xlog.Debug("mitm: read request", "host", host, "error", err)
			}
			return
		}
		rw := newConnResponseWriter(tlsConn, req)
		handler.ServeHTTP(rw, req)
		rw.finish()
		if req.Close || rw.closeAfter {
			return
		}
	}
}

func normalizeHostPort(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return host + ":443"
}
