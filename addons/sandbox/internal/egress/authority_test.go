// SPDX-License-Identifier: MIT
package egress_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
)

var _ = Describe("TestAddonSettingsAuthorityFreshConnections", func() {
	var server *httptest.Server
	var accepted atomic.Int32
	BeforeEach(func() {
		accepted.Store(0)
		server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				accepted.Add(1)
			}
		}
		server.StartTLS()
		DeferCleanup(func() { server.Close() })
	})
	// The existing fixture resolves only test names to a bounded loopback server.
	// TreatAsPublic is test-only; no production guard or dial hook is changed.
	request := func(c net.Conn, br *bufio.Reader, port int, connect bool) *http.Response {
		if connect {
			_, _ = fmt.Fprintf(c, "CONNECT public.test:%d HTTP/1.1\r\nHost: public.test:%d\r\n\r\n", port, port)
		} else {
			_, _ = fmt.Fprintf(c, "GET http://public.test:%d/ HTTP/1.1\r\nHost: public.test:%d\r\n\r\n", port, port)
		}
		return readResponse(c, br, nil)
	}
	for _, tunnel := range []bool{false, true} {
		for _, unavailable := range []bool{false, true} {
			for _, keepGuest := range []bool{false, true} {
				if tunnel && keepGuest {
					continue
				}
				It(fmt.Sprintf("denies immediately after allow: CONNECT=%t outage=%t keepGuest=%t", tunnel, unavailable, keepGuest), func() {
					port := portOf(server.URL)
					if !tunnel {
						server.Close()
						server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
						port = portOf(server.URL)
					}
					f := newFixture(port, allow, egress.Limits{})
					defer f.proxy.Begin(context.Background(), "alice", "c1", "same-operation")()
					c := f.dial()
					br := bufio.NewReader(c)
					resp := request(c, br, port, tunnel)
					Expect(resp.StatusCode).To(Equal(http.StatusOK))
					var stream *tls.Conn
					if tunnel {
						stream = tls.Client(&bufConn{Conn: c, r: br}, &tls.Config{ServerName: "public.test", InsecureSkipVerify: true}) //nolint:gosec // local test certificate
						Expect(stream.Handshake()).To(Succeed())
					} else {
						Expect(body(resp)).To(Equal("ok"))
					}
					f.host.mu.Lock()
					if unavailable {
						f.host.err = errors.New("owner unavailable")
					} else {
						f.host.decide = func(egress.CheckArgs) egress.Verdict { return egress.Verdict{Action: "block", Reason: "ceiling"} }
					}
					f.host.mu.Unlock()
					if !keepGuest {
						c = f.dial()
						br = bufio.NewReader(c)
					}
					denied := request(c, br, port, tunnel)
					Expect(denied.StatusCode).To(Equal(http.StatusForbidden))
					if unavailable {
						Expect(body(denied)).To(ContainSubstring("unavailable"))
					}
					Expect(f.host.nChecks()).To(Equal(2))
					// An already-authorized TLS stream remains usable after policy changes.
					if stream != nil {
						_, err := fmt.Fprint(stream, "GET / HTTP/1.1\r\nHost: public.test\r\nConnection: close\r\n\r\n")
						Expect(err).NotTo(HaveOccurred())
						Expect(body(readResponse(stream, bufio.NewReader(stream), nil))).To(Equal("ok"))
						Expect(accepted.Load()).To(Equal(int32(1)))
					}
				})
			}
		}
	}
	for _, tunnel := range []bool{false, true} {
		It(fmt.Sprintf("does not reuse a consumed grant across repeated or concurrent connections: CONNECT=%t", tunnel), func() {
			port := portOf(server.URL)
			if !tunnel {
				server.Close()
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
				port = portOf(server.URL)
			}
			var grants atomic.Int32
			f := newFixture(port, func(egress.CheckArgs) egress.Verdict {
				if grants.Add(1) == 1 {
					return egress.Verdict{Action: "allow", Reason: "once"}
				}
				return egress.Verdict{Action: "block", Reason: "denied"}
			}, egress.Limits{})
			defer f.proxy.Begin(context.Background(), "alice", "c1", "same-operation")()
			first := f.dial()
			resp := request(first, bufio.NewReader(first), port, tunnel)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			if !tunnel {
				Expect(body(resp)).To(Equal("ok"))
			}
			_ = first.Close()
			// Pre-open the guest sockets, then dispatch requests together after consumption.
			guests := []net.Conn{f.dial(), f.dial(), f.dial()}
			codes := make(chan int, len(guests))
			for _, c := range guests {
				go func() { defer GinkgoRecover(); codes <- request(c, bufio.NewReader(c), port, tunnel).StatusCode }()
			}
			for range guests {
				Eventually(codes).Should(Receive(Equal(http.StatusForbidden)))
			}
			Expect(f.host.nChecks()).To(Equal(4))
		})
	}
})
