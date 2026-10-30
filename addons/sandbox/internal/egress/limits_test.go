// SPDX-License-Identifier: MIT
package egress_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
)

var _ = Describe("the limits of the proxy", func() {
	var tlsSrv, plainSrv *httptest.Server
	BeforeEach(func() {
		tlsSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secure") }))
		DeferCleanup(tlsSrv.Close)
		plainSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, _ := io.Copy(io.Discard, r.Body)
			_, _ = fmt.Fprintf(w, "got %d", n)
		}))
		DeferCleanup(plainSrv.Close)
	})

	// tunnel opens an allowed TLS tunnel and keeps it open.
	tunnel := func(f fixture, port int) (*tls.Conn, int) {
		c := f.dial()
		_, _ = fmt.Fprintf(c, "CONNECT public.test:%d HTTP/1.1\r\nHost: public.test:%d\r\n\r\n", port, port)
		br := bufio.NewReader(c)
		resp := readResponse(c, br, &http.Request{Method: http.MethodConnect})
		if resp.StatusCode != http.StatusOK {
			return nil, resp.StatusCode
		}
		tc := tls.Client(&bufConn{Conn: c, r: br}, &tls.Config{ServerName: "public.test", InsecureSkipVerify: true}) //nolint:gosec // a test server
		Expect(tc.Handshake()).To(Succeed())
		return tc, resp.StatusCode
	}

	It("caps the open tunnels of a sandbox and of the process", func() {
		port := portOf(tlsSrv.URL)
		f := newFixture(port, allow, egress.Limits{TunnelsPerSandbox: 2, TunnelsTotal: 3})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		_, code1 := tunnel(f, port)
		_, code2 := tunnel(f, port)
		_, code3 := tunnel(f, port)
		Expect([]int{code1, code2, code3}).To(Equal([]int{200, 200, 503}), "two per sandbox")

		// Another sandbox shares the process cap of three.
		other := f.proxy.Begin(context.Background(), "bob", "c2", "op2")
		defer other()
		h := f.proxy.Ports("bob", "c2")[egress.Port]
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = ln.Close() }()
		go func() {
			for {
				side, err := ln.Accept()
				if err != nil {
					return
				}
				go h(context.Background(), side)
			}
		}()
		codes := []int{}
		for range 2 {
			c, err := net.Dial("tcp", ln.Addr().String())
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = c.Close() })
			_, _ = fmt.Fprintf(c, "CONNECT public.test:%d HTTP/1.1\r\nHost: public.test:%d\r\n\r\n", port, port)
			br := bufio.NewReader(c)
			resp := readResponse(c, br, &http.Request{Method: http.MethodConnect})
			codes = append(codes, resp.StatusCode)
			if resp.StatusCode == http.StatusOK {
				tc := tls.Client(&bufConn{Conn: c, r: br}, &tls.Config{ServerName: "public.test", InsecureSkipVerify: true}) //nolint:gosec // a test server
				Expect(tc.Handshake()).To(Succeed())
			}
		}
		Expect(codes).To(Equal([]int{200, 503}), "three in the process")
	})

	It("closes a tunnel with no bytes for the idle time", func() {
		port := portOf(tlsSrv.URL)
		f := newFixture(port, allow, egress.Limits{Idle: 300 * time.Millisecond})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		tc, code := tunnel(f, port)
		Expect(code).To(Equal(200))
		// The fixture clock is frozen and idle time is measured on it, so it
		// moves forward until the proxy closes the tunnel (bytes the server
		// sends after the handshake reset the idle time).
		Eventually(func() bool {
			f.advance(time.Second)
			_ = tc.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			_, err := tc.Read(make([]byte, 1))
			return err != nil && !strings.Contains(err.Error(), "timeout")
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue(), "closed by the proxy")
	})

	It("cuts an upload at the per-call cap", func() {
		f := newFixture(0, allow, egress.Limits{UpBytes: 1000})
		end := f.proxy.Begin(context.Background(), "alice", "c1", "op1")
		defer end()
		port := portOf(plainSrv.URL)
		c := f.dial()
		body := strings.Repeat("u", 5000)
		_, _ = fmt.Fprintf(c, "POST http://public.test:%d/ HTTP/1.1\r\nHost: public.test:%d\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", port, port, len(body), body)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		got, _ := io.ReadAll(c)
		Expect(string(got)).NotTo(ContainSubstring("got 5000"), "the whole upload must not reach the server")
	})
})
