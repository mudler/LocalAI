// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
)

var _ = Describe("The backend client factory", func() {
	Describe("without a worker tunnel dialer", func() {
		It("refuses to build a client for a node rather than dialling its address", func() {
			// The whole point. A factory that answered here with a client
			// pointed at the raw address would work on a single-host developer
			// setup and fail against every worker that has no inbound port,
			// which is the worst way for this to behave.
			f := &tokenClientFactory{token: "tok"}
			_, err := f.NewClientForNode("node-1", "10.0.0.1:41000", false)
			Expect(err).To(MatchError(ErrNoWorkerDialer))
		})

		It("offers no direct-dial constructor for anything to reach for", func() {
			// Structural, not documented. A NewClient alongside NewClientForNode
			// would be reachable from every call site that holds an address,
			// which is all of them, and reintroducing the bypass would then be
			// a one-word edit that compiles and passes every other spec.
			var factory any = &tunnelClientFactory{}
			_, hasDirectDial := factory.(interface {
				NewClient(address string, parallel bool) grpcpkg.Backend
			})
			Expect(hasDirectDial).To(BeFalse())
		})

		It("refuses to be constructed at all", func() {
			_, err := NewTunnelClientFactory("tok", nil)
			Expect(err).To(MatchError(ErrNoWorkerDialer))
		})
	})

	Describe("with a worker tunnel dialer", func() {
		It("builds a client that reaches the backend through the node's dialer", func() {
			// The proof is that the client's transport is the one this factory
			// was given: it carries bytes from a listener that the address in
			// the request never names.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = listener.Close() })

			asked := make(chan string, 4)
			f, err := NewTunnelClientFactory("", func(nodeID string) func(ctx context.Context, addr string) (net.Conn, error) {
				return func(ctx context.Context, addr string) (net.Conn, error) {
					asked <- nodeID + "|" + addr
					var d net.Dialer
					return d.DialContext(ctx, "tcp", listener.Addr().String())
				}
			})
			Expect(err).ToNot(HaveOccurred())

			client, err := f.NewClientForNode("node-1", "10.255.255.1:41000", false)
			Expect(err).ToNot(HaveOccurred())

			// The address is unroutable on purpose: only a client that used the
			// dialer can reach anything at all. The health check itself fails,
			// because nothing on the far side speaks gRPC; what it proves is
			// which transport was asked.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				defer GinkgoRecover()
				_, _ = client.HealthCheck(ctx)
			}()
			Eventually(asked, "10s").Should(Receive(Equal("node-1|10.255.255.1:41000")))
		})

		It("refuses a request with no node id", func() {
			f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
				var d net.Dialer
				return func(ctx context.Context, addr string) (net.Conn, error) {
					return d.DialContext(ctx, "tcp", addr)
				}
			})
			Expect(err).ToNot(HaveOccurred())
			_, err = f.NewClientForNode("", "10.0.0.1:41000", false)
			Expect(err).To(MatchError(ErrNoWorkerDialer))
		})

		It("refuses when the dialer has none for that node", func() {
			f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			_, err = f.NewClientForNode("node-1", "10.0.0.1:41000", false)
			Expect(err).To(MatchError(ErrNoWorkerDialer))
		})
	})
})
