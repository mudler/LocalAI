package sbtest

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func proxyRequest(p *ConnectProxy, req string) (int, net.Conn) {
	a, b := net.Pipe()
	go p.Handler(context.Background(), b)
	go func() { _, _ = io.WriteString(a, req) }()
	resp, err := http.ReadResponse(bufio.NewReader(a), nil)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, a
}

var _ = Describe("port handlers", func() {
	It("EchoHandler echoes what it reads", func() {
		a, b := net.Pipe()
		go EchoHandler(context.Background(), b)
		_, err := io.WriteString(a, "ping")
		Expect(err).NotTo(HaveOccurred())
		buf := make([]byte, 4)
		_, err = io.ReadFull(a, buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf)).To(Equal("ping"))
		_ = a.Close()
	})

	It("ConnectProxy allows only the listed hosts and records what it saw", func() {
		up1, up2 := net.Pipe()
		p := &ConnectProxy{Allow: []string{"allowed.example:443"}, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return up1, nil
		}}
		code, c := proxyRequest(p, "CONNECT denied.example:443 HTTP/1.1\r\nHost: denied.example:443\r\n\r\n")
		Expect(code).To(Equal(http.StatusForbidden), "denied")
		_ = c.Close()
		code, c = proxyRequest(p, "GET http://plain.example/ HTTP/1.1\r\nHost: plain.example\r\n\r\n")
		Expect(code).To(Equal(http.StatusMethodNotAllowed), "get")
		_ = c.Close()
		code, c = proxyRequest(p, "CONNECT allowed.example:443 HTTP/1.1\r\nHost: allowed.example:443\r\n\r\n")
		Expect(code).To(Equal(http.StatusOK), "allowed")
		go func() { _, _ = io.WriteString(c, "hello") }()
		buf := make([]byte, 5)
		_, err := io.ReadFull(up2, buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf)).To(Equal("hello"))
		_ = c.Close()
		Expect(p.Seen()).To(Equal([]string{"denied.example:443", "allowed.example:443"}))
		Expect(strings.HasSuffix(p.Seen()[1], ":443")).To(BeTrue(), "seen lost the port")
	})
})
