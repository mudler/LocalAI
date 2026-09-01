// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"fmt"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// proberFactory hands the prober one client, and records what it was asked for.
type proberFactory struct {
	client grpc.Backend
	err    error
	asked  []string
}

func (f *proberFactory) NewClientForNode(nodeID, address string, _ bool) (grpc.Backend, error) {
	f.asked = append(f.asked, nodeID+"|"+address)
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

var _ = Describe("the reconciler's gRPC model prober", func() {
	// The two lines that decide whether a row survives, both previously
	// untested. Everything else in the reaper is driven through fakeProber,
	// which means the mapping from a real client to a ProbeOutcome had nothing
	// holding it at all.
	probe := func(f *proberFactory) ProbeOutcome {
		GinkgoHelper()
		return grpcModelProber{clients: f}.Probe(context.Background(), "node-1", "10.0.0.1:9001")
	}

	It("answers ProbeUnknown when no client can be built for the node", func() {
		Expect(probe(&proberFactory{err: ErrNoWorkerDialer})).To(Equal(ProbeUnknown))
	})

	It("answers ProbeUnknown when the client was built and the tunnel dial failed", func() {
		// The likelier half. ProbeUnreachable here would delete the row after
		// probeFailuresBeforeReap passes of a peer link that was merely
		// restarting, and the backend would still be running the model.
		Expect(probe(&proberFactory{client: &fakeBackendClient{
			healthy: false,
			err:     fmt.Errorf("rpc error: code = Unavailable"),
			dialErr: fmt.Errorf("%w: %w", cluster.ErrNoRoute, cluster.ErrPeerUnreachable),
		}})).To(Equal(ProbeUnknown))
	})

	It("asks for the client by NODE, not by address alone", func() {
		f := &proberFactory{client: &fakeBackendClient{healthy: true}}
		Expect(probe(f)).To(Equal(ProbeAlive))
		Expect(f.asked).To(ContainElement("node-1|10.0.0.1:9001"))
	})

	It("answers ProbeAlive for a healthy backend it reached", func() {
		Expect(probe(&proberFactory{client: &fakeBackendClient{healthy: true}})).To(Equal(ProbeAlive))
	})

	It("still answers ProbeUnreachable for a dead backend on a worker it reached", func() {
		// The other direction. The new check must not turn the reaper off: a
		// backend that answered "unhealthy" over a working transport is a ghost
		// and its row should go.
		Expect(probe(&proberFactory{client: &fakeBackendClient{healthy: false}})).To(Equal(ProbeUnreachable))
	})

	It("does not report a transport that recovered", func() {
		// LastDialError is cleared by a successful dial, so a client that
		// failed once and then reconnected must not keep reading as
		// unroutable; otherwise a row could never be reaped again after one
		// blip on that client.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = listener.Close() })

		attempt := 0
		f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return func(ctx context.Context, _ string) (net.Conn, error) {
				attempt++
				if attempt == 1 {
					return nil, errors.New("first dial fails")
				}
				return d.DialContext(ctx, "tcp", listener.Addr().String())
			}
		})
		Expect(err).ToNot(HaveOccurred())
		client, err := f.NewClientForNode("node-1", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())

		_, _ = client.HealthCheck(context.Background())
		Expect(unroutable(client)).ToNot(BeNil())

		// gRPC re-dials on the next call; the listener now accepts.
		Eventually(func() error {
			_, _ = client.HealthCheck(context.Background())
			return unroutable(client)
		}, "20s").Should(BeNil())
	})
})
