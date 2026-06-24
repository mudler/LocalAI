package nodes

import (
	"errors"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeConn stands in for *messaging.Client, which cannot be constructed without
// a live NATS server. Only IsConnected() is consulted by the readiness probe.
type fakeConn struct{ connected bool }

func (f *fakeConn) IsConnected() bool { return f.connected }

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

	Describe("NATSReadiness", func() {
		It("reports ready while the NATS connection is up", func() {
			Expect(NATSReadiness(&fakeConn{connected: true})()).To(Succeed())
		})

		It("reports not-ready once the NATS connection drops", func() {
			// This is the failure mode issue #10987 is about: the process is
			// up and the port is bound, but the worker can receive no work.
			err := NATSReadiness(&fakeConn{connected: false})()
			Expect(err).To(MatchError(ContainSubstring("NATS")))
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
			ready.Set(func() error { return errors.New("NATS disconnected") })
			Expect(get("/readyz")).To(Equal(http.StatusServiceUnavailable))
		})

		It("keeps /healthz at 200 even when readiness fails", func() {
			// Liveness is deliberately independent of readiness: a worker whose
			// NATS link is briefly down must not be killed and restarted, or a
			// NATS outage turns into a restart storm across every worker.
			ready.Set(func() error { return errors.New("NATS disconnected") })
			Expect(get("/healthz")).To(Equal(http.StatusOK))
		})
	})
})
