package localai

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes"
)

var _ = Describe("Backend logs of a worker that holds a tunnel", func() {
	Describe("the host of the request", func() {
		It("names the reserved host for a worker with no address at all", func() {
			host, ok := workerLogsHost(&nodes.BackendNode{ID: "n1"})
			Expect(ok).To(BeTrue())
			Expect(host).To(Equal("n1.worker.invalid"))
			Expect(nodes.IsTunnelOnlyHost(host)).To(BeTrue())
		})

		It("keeps the HTTP address of a worker that has one", func() {
			host, ok := workerLogsHost(&nodes.BackendNode{ID: "n1", Address: "10.0.0.1:50051", HTTPAddress: "10.0.0.1:50050"})
			Expect(ok).To(BeTrue())
			Expect(host).To(Equal("10.0.0.1:50050"))
		})

		It("still refuses a worker that has a gRPC address and no HTTP server, as before", func() {
			_, ok := workerLogsHost(&nodes.BackendNode{ID: "n1", Address: "10.0.0.1:50051"})
			Expect(ok).To(BeFalse())
		})
	})

	Describe("the proxy of the environment", func() {
		dial := func(string) func(context.Context, string, string) (net.Conn, error) { return nil }

		It("is not used to reach a worker through its tunnel, so the dialer gets the stream", func() {
			Expect(workerTransport(dial, "n1", "n1.worker.invalid").Proxy).To(BeNil())
		})

		It("is still used for a worker that has an address, as before", func() {
			Expect(workerTransport(dial, "n1", "10.0.0.1:50050").Proxy).ToNot(BeNil())
		})

		It("sends the request of a worker without an address to the dialer even when the environment names a proxy", func() {
			// The proxy of the environment is read once for the process, so this
			// spec does not rely on it: it checks that the transport has none.
			var (
				srv   = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "logs for "+r.Host) }))
				mu    sync.Mutex
				dials []string
			)
			DeferCleanup(srv.Close)
			dialer := func(string) func(context.Context, string, string) (net.Conn, error) {
				return func(ctx context.Context, _, addr string) (net.Conn, error) {
					mu.Lock()
					dials = append(dials, addr)
					mu.Unlock()
					var d net.Dialer
					return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
				}
			}
			resp, err := proxyHTTPToWorker(context.Background(), dialer, "n1", "n1.worker.invalid", "/v1/backend-logs", "token")
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			Expect(string(body)).To(Equal("logs for n1.worker.invalid"))
			mu.Lock()
			defer mu.Unlock()
			Expect(dials).To(HaveLen(1))
			Expect(dials[0]).To(HavePrefix("n1.worker.invalid"), "the dialer was asked for the worker, not for a proxy")
		})
	})
})
