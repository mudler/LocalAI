package nodes

import (
	"errors"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeTunnel stands in for *worker.Tunnel, which this package cannot import
// (worker imports nodes). Only Connected() is consulted by the readiness probe.
type fakeTunnel struct{ connected bool }

func (f fakeTunnel) Connected() bool { return f.connected }

var _ = Describe("WorkerReadiness", func() {
	Describe("the gate itself", func() {
		It("reports ready when no probe has been installed yet", func() {
			// Fail open: an embedder (or the frontend) that never wires a probe
			// keeps the historical always-ready behaviour.
			r := &WorkerReadiness{}
			Expect(r.Check()).To(Succeed())
		})

		It("surfaces the installed probe's error", func() {
			r := &WorkerReadiness{}
			r.Set(func() error { return errors.New("boom") })
			Expect(r.Check()).To(MatchError(ContainSubstring("boom")))
		})

		It("lets a later Set replace an earlier probe", func() {
			r := &WorkerReadiness{}
			r.Set(func() error { return errors.New("boom") })
			r.Set(func() error { return nil })
			Expect(r.Check()).To(Succeed())
		})
	})

	Describe("TunnelReadiness", func() {
		It("reports ready once the tunnel holds a session", func() {
			Expect(TunnelReadiness(fakeTunnel{connected: true})()).To(Succeed())
		})

		It("reports not ready while the tunnel holds no session", func() {
			// This is the failure mode issue #10987 is about: the process is
			// up and the port is bound, but nothing can reach this worker,
			// because every request the frontend makes of it arrives over the
			// tunnel.
			Expect(TunnelReadiness(fakeTunnel{connected: false})()).To(MatchError(ErrTunnelDisconnected))
		})

		It("reports not ready for a nil tunnel rather than panicking", func() {
			// Run installs the probe after StartTunnel, so a nil here means a
			// wiring mistake. Reporting it beats taking the HTTP handler
			// goroutine down with it.
			Expect(TunnelReadiness(nil)()).To(MatchError(ErrTunnelDisconnected))
		})
	})

	Describe("the file transfer server probes", func() {
		var (
			srv     *http.Server
			baseURL string
			ready   *WorkerReadiness
		)

		BeforeEach(func() {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			ready = &WorkerReadiness{}
			srv, err = StartFileTransferServerWithReadiness(
				lis, GinkgoT().TempDir(), GinkgoT().TempDir(), GinkgoT().TempDir(),
				"tok", 1024, ready, nil,
			)
			Expect(err).ToNot(HaveOccurred())
			baseURL = "http://" + lis.Addr().String()
			DeferCleanup(func() { ShutdownFileTransferServer(srv) })
		})

		get := func(path string) int {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(baseURL + path)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			return resp.StatusCode
		}

		It("serves /readyz 200 while the probe reports ready", func() {
			ready.Set(func() error { return nil })
			Expect(get("/readyz")).To(Equal(http.StatusOK))
		})

		It("serves /readyz 503 once the probe reports not-ready", func() {
			ready.Set(func() error { return errors.New("tunnel disconnected") })
			Expect(get("/readyz")).To(Equal(http.StatusServiceUnavailable))
		})

		It("keeps /healthz at 200 even when readiness fails", func() {
			// Liveness is deliberately independent of readiness: a worker whose
			// tunnel is briefly down must not be killed and restarted, or one
			// frontend restart turns into a restart storm across every worker.
			ready.Set(func() error { return errors.New("tunnel disconnected") })
			Expect(get("/healthz")).To(Equal(http.StatusOK))
		})
	})
})
