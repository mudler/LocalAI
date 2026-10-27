package sbtest

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestEchoHandler(t *testing.T) {
	a, b := net.Pipe()
	go EchoHandler(context.Background(), b)
	if _, err := io.WriteString(a, "ping"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(a, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
	_ = a.Close()
}

func proxyRequest(t *testing.T, p *ConnectProxy, req string) (int, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	go p.Handler(context.Background(), b)
	go func() { _, _ = io.WriteString(a, req) }()
	resp, err := http.ReadResponse(bufio.NewReader(a), nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, a
}

func TestConnectProxy(t *testing.T) {
	up1, up2 := net.Pipe()
	p := &ConnectProxy{Allow: []string{"allowed.example:443"}, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return up1, nil
	}}
	if code, c := proxyRequest(t, p, "CONNECT denied.example:443 HTTP/1.1\r\nHost: denied.example:443\r\n\r\n"); code != http.StatusForbidden {
		t.Fatalf("denied: %d", code)
	} else {
		_ = c.Close()
	}
	if code, c := proxyRequest(t, p, "GET http://plain.example/ HTTP/1.1\r\nHost: plain.example\r\n\r\n"); code != http.StatusMethodNotAllowed {
		t.Fatalf("get: %d", code)
	} else {
		_ = c.Close()
	}
	code, c := proxyRequest(t, p, "CONNECT allowed.example:443 HTTP/1.1\r\nHost: allowed.example:443\r\n\r\n")
	if code != http.StatusOK {
		t.Fatalf("allowed: %d", code)
	}
	go func() { _, _ = io.WriteString(c, "hello") }()
	buf := make([]byte, 5)
	if _, err := io.ReadFull(up2, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("upstream got %q, %v", buf, err)
	}
	_ = c.Close()
	if got := p.Seen(); !reflect.DeepEqual(got, []string{"denied.example:443", "allowed.example:443"}) {
		t.Fatalf("seen = %v", got)
	}
	if !strings.HasSuffix(p.Seen()[1], ":443") {
		t.Fatal("seen lost the port")
	}
}
