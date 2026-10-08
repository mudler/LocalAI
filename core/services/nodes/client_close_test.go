package nodes

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The clients of a tunnel carrier keep idle streams of the tunnel. When the
// carrier is closed they must give them back, and not wait for the idle timeout
// of 90 seconds.
var _ = Describe("Closing the clients of the tunnel carrier", func() {
	// server counts the connections it has open.
	server := func() (*httptest.Server, *atomic.Int32) {
		GinkgoHelper()
		var open atomic.Int32
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{}"))
		}))
		srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
			switch s {
			case http.StateNew:
				open.Add(1)
			case http.StateClosed, http.StateHijacked:
				open.Add(-1)
			}
		}
		srv.Start()
		DeferCleanup(srv.Close)
		return srv, &open
	}

	dialTo := func(srv *httptest.Server) WorkerNetDialerFor {
		return func(string) func(context.Context, string, string) (net.Conn, error) {
			return func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
			}
		}
	}

	It("drops the idle streams of every node held by the control client", func() {
		srv, open := server()
		client := NewControlClient(dialTo(srv), "")
		for _, node := range []string{"n1", "n2"} {
			Expect(client.Call(context.Background(), node, workerctl.VerbBackendList, struct{}{}, &workerctl.BackendListReply{})).To(Succeed())
		}
		Eventually(open.Load).Should(Equal(int32(2)), "one idle stream for each node")

		client.Close()
		Eventually(open.Load, 5*time.Second).Should(BeZero())
	})

	It("drops the idle streams of every node held by the file stager", func() {
		srv, open := server()
		stager := NewHTTPFileStager(func(string) (string, error) { return srv.Listener.Addr().String(), nil }, "", dialTo(srv))
		for _, node := range []string{"n1", "n2"} {
			resp, err := mustClient(stager, node).Get(srv.URL)
			Expect(err).ToNot(HaveOccurred())
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		Eventually(open.Load).Should(Equal(int32(2)))

		stager.Close()
		Eventually(open.Load, 5*time.Second).Should(BeZero())
	})

	It("is safe on nil and when called twice", func() {
		var c *ControlClient
		var s *HTTPFileStager
		Expect(func() { c.Close(); s.Close() }).ToNot(Panic())
		client := NewControlClient(nil, "")
		Expect(func() { client.Close(); client.Close() }).ToNot(Panic())
	})
})

func mustClient(s *HTTPFileStager, node string) *http.Client {
	c, err := s.clientFor(node)
	Expect(err).ToNot(HaveOccurred())
	return c
}
