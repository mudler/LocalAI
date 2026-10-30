package sbtest

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

// EchoHandler writes back what it reads until the guest closes the
// connection.
func EchoHandler(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_, _ = io.Copy(conn, conn)
}

// ConnectProxy is a minimal HTTP CONNECT proxy for tests. It records the
// target of every request and connects only to the targets in Allow
// ("host:port"). It refuses other methods with 405.
type ConnectProxy struct {
	Allow []string
	// Dial connects to an allowed target. Nil means a net.Dialer with a
	// 10 second timeout.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	mu   sync.Mutex
	seen []string
}

// Seen returns the targets of the requests so far, in order.
func (p *ConnectProxy) Seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// Handler is the sandbox.PortHandler of the proxy.
func (p *ConnectProxy) Handler(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = io.WriteString(conn, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	p.mu.Lock()
	p.seen = append(p.seen, req.Host)
	p.mu.Unlock()
	if !slices.Contains(p.Allow, req.Host) {
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
		return
	}
	dial := p.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}
	up, err := dial(ctx, "tcp", req.Host)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	_ = conn.SetReadDeadline(time.Time{})
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, up); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
