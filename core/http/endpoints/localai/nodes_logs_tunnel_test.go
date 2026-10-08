package localai

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
		var (
			srv   *httptest.Server
			mu    sync.Mutex
			dials []string
		)

		BeforeEach(func() {
			dials = nil
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "logs for "+r.Host)
			}))
			DeferCleanup(srv.Close)
			// A proxy that nothing listens on. A request that honours it fails.
			GinkgoT().Setenv("HTTP_PROXY", "http://127.0.0.1:1")
			GinkgoT().Setenv("http_proxy", "http://127.0.0.1:1")
			GinkgoT().Setenv("NO_PROXY", "")
			GinkgoT().Setenv("no_proxy", "")
		})

		dialer := func() nodes.WorkerNetDialerFor {
			return func(string) func(context.Context, string, string) (net.Conn, error) {
				return func(ctx context.Context, _, addr string) (net.Conn, error) {
					mu.Lock()
					dials = append(dials, addr)
					mu.Unlock()
					var d net.Dialer
					return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
				}
			}
		}

		It("is not used to reach a worker through its tunnel, so the dialer gets the stream", func() {
			resp, err := proxyHTTPToWorker(context.Background(), dialer(), "n1", "n1.worker.invalid", "/v1/backend-logs", "token")
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			Expect(string(body)).To(Equal("logs for n1.worker.invalid"))
			mu.Lock()
			defer mu.Unlock()
			Expect(dials).To(HaveLen(1))
			Expect(dials[0]).To(HavePrefix("n1.worker.invalid"), "the dialer was asked for the worker, not for a proxy")
		})

		It("is still used for a worker that has an address, as before", func() {
			_, err := proxyHTTPToWorker(context.Background(), dialer(), "n1", "10.0.0.1:50050", "/v1/backend-logs", "token")
			Expect(err).ToNot(HaveOccurred())
			mu.Lock()
			defer mu.Unlock()
			Expect(dials).To(HaveLen(1))
			Expect(strings.HasPrefix(dials[0], "127.0.0.1:1")).To(BeTrue(), "with a proxy in the environment the dial goes to the proxy: %v", dials)
		})
	})
})
