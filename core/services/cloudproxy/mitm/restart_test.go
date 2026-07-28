package mitm

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// noopHandler is the simplest InterceptHandler that satisfies NewServer.
// We only exercise Start/Stop lifecycle here — no requests go through.
func noopHandler(_ http.ResponseWriter, _ *http.Request, _ string) {}

func newTestServer(addr string, hosts []string) *Server {
	ca, err := NewInMemoryCA()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "NewInMemoryCA")
	srv, err := NewServer(Config{
		Addr:           addr,
		CA:             ca,
		InterceptHosts: hosts,
		Handler:        noopHandler,
	})
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "NewServer")
	return srv
}

// Server_StopIdempotent: calling Stop twice (and Stop without
// Start) must not panic or deadlock. The application's RestartMITM
// path is sensitive to this — it always calls Stop before Start, even
// when the server is already stopped.
var _ = Describe("Server", func() {
	It("Stop is idempotent", func() {
		srv := newTestServer("127.0.0.1:0", nil)
		srv.Stop() // never started
		srv.Stop() // double-stop after never-started

		srv2 := newTestServer("127.0.0.1:0", nil)
		Expect(srv2.Start()).To(Succeed())
		srv2.Stop()
		srv2.Stop() // second Stop after Start+Stop
	})

	// Server_RestartCycle: two sequential Server lifecycles on the
	// same address must rebind cleanly, the new listener must accept
	// connections, and the new allowlist must take effect — the shape
	// RestartMITM relies on.
	It("restart cycle rebinds and swaps allowlist", func() {
		// First, find a free port we can rebind to.
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred(), "probe listen")
		addr := probe.Addr().String()
		_ = probe.Close()

		srv1 := newTestServer(addr, []string{"first.example.com"})
		if err := srv1.Start(); err != nil {
			// Port could have been recycled between probe close and Start.
			// Skip rather than flake — the production path uses dynamic
			// addrs anyway.
			Skip(fmt.Sprintf("could not bind probed addr: %v", err))
		}
		Expect(strings.HasPrefix(srv1.Addr(), "127.0.0.1:")).To(BeTrue(), "Addr() = %q, want 127.0.0.1:* prefix", srv1.Addr())
		srv1.Stop()

		// Now bring up a second server on the same addr with a different
		// allowlist — mirrors the RestartMITM-with-edited-hosts path.
		srv2 := newTestServer(addr, []string{"second.example.com"})
		if err := srv2.Start(); err != nil {
			// SO_REUSEADDR is not set; brief TIME_WAIT collisions are
			// possible on slow CI runners. Retry once on a fresh port so
			// the test still exercises the "different hosts" property.
			srv2 = newTestServer("127.0.0.1:0", []string{"second.example.com"})
			Expect(srv2.Start()).To(Succeed(), "second Start (fresh port fallback)")
		}
		defer srv2.Stop()

		// Smoke: the new listener accepts a TCP connection.
		conn, err := net.Dial("tcp", srv2.Addr())
		Expect(err).NotTo(HaveOccurred(), "dial restarted listener")
		_ = conn.Close()

		// Allowlist swap took effect: the new server intercepts
		// "second.example.com" (and not the old "first.example.com").
		Expect(srv2.shouldIntercept("second.example.com")).To(BeTrue(), "second server did not pick up the new InterceptHosts")
		Expect(srv2.shouldIntercept("first.example.com")).To(BeFalse(), "second server still has the first server's allowlist")
	})

	// Server_AddrBeforeStart: Addr() pre-Start returns the configured
	// address rather than panicking on a nil listener. The admin status
	// endpoint reads it under MITMServer() — when an admin queries between
	// configuration and Start, the response should still render cleanly.
	It("Addr before start returns configured address", func() {
		srv := newTestServer(":12345", nil)
		Expect(srv.Addr()).To(Equal(":12345"))
	})
})
