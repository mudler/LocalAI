// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// The reviewer's spec, plus the production shape it was pointing at.
//
// The guard added at the fourth reap site asks the client whether the TRANSPORT
// failed. In production that client is not the one the factory built: SmartRouter
// hands out result.Client, which is an *InFlightTrackingClient, over a
// *FileStagingClient whenever a stager is configured. Both embed grpc.Backend,
// which does not declare LastDialError, so a type assertion on the outermost
// type read nil and the guard was inert for exactly the models the router
// produces. Every spec that constructed a raw client by hand passed anyway.
//
// This is the third time in this task that a correct fix was disarmed by a
// layer further out, which is why the mechanism is now one walker rather than a
// per-caller assertion.
var _ = Describe("the transport answer through the wrappers the router builds", func() {
	var (
		cause error
		raw   grpc.Backend
	)

	BeforeEach(func() {
		cause = errors.New("cluster: no route from this replica to that worker")
		f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
			return func(context.Context, string) (net.Conn, error) { return nil, cause }
		})
		Expect(err).ToNot(HaveOccurred())
		raw, err = f.NewClientForNode("X", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())

		// Provoke one dial so there is something to report.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, _ = raw.HealthCheck(ctx)
	})

	It("the raw factory client reports, as designed", func() {
		Expect(unroutable(raw)).To(MatchError(ErrWorkerUnroutable))
	})

	It("reports through the in-flight tracker, which is what RouteResult.Client is", func() {
		tracked := NewInFlightTrackingClient(raw, &fakeModelRouter{}, "X", "m", 0)
		Expect(unroutable(tracked)).To(MatchError(ErrWorkerUnroutable))
	})

	It("reports through the file staging client, which buildClientForAddr adds", func() {
		staged := NewFileStagingClient(raw, nil, "X")
		Expect(unroutable(staged)).To(MatchError(ErrWorkerUnroutable))
	})

	It("reports through BOTH, nested the way production nests them", func() {
		// buildClientForAddr wraps in staging, newRouteResult wraps that in
		// tracking, model_router puts the result on the cached model, and
		// pkg/model's checkIsLoaded asks it. Two layers, and a walker that
		// stopped at one would still be wrong here.
		nested := NewInFlightTrackingClient(NewFileStagingClient(raw, nil, "X"), &fakeModelRouter{}, "X", "m", 0)
		Expect(unroutable(nested)).To(MatchError(ErrWorkerUnroutable))
	})

	It("still reports nothing through the wrappers when the dial succeeded", func() {
		// The other direction, so forwarding cannot pass by always answering
		// "unroutable": a backend that genuinely died must still be reapable
		// through the same wrappers.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = listener.Close() })

		f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return func(ctx context.Context, _ string) (net.Conn, error) {
				return d.DialContext(ctx, "tcp", listener.Addr().String())
			}
		})
		Expect(err).ToNot(HaveOccurred())
		live, err := f.NewClientForNode("X", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())
		_, _ = live.HealthCheck(context.Background())

		nested := NewInFlightTrackingClient(NewFileStagingClient(live, nil, "X"), &fakeModelRouter{}, "X", "m", 0)
		Expect(unroutable(nested)).To(BeNil())
	})

	It("keeps the cluster condition matchable through the wrappers", func() {
		// Not merely "something failed". The five conditions have to survive
		// the decorators as well as gRPC, or the consumers are guessing again.
		routed := errors.New("x")
		f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
			return func(context.Context, string) (net.Conn, error) { return nil, routed }
		})
		Expect(err).ToNot(HaveOccurred())
		c, err := f.NewClientForNode("X", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())
		routed = cluster.ErrNoRoute
		_, _ = c.HealthCheck(context.Background())

		nested := NewInFlightTrackingClient(NewFileStagingClient(c, nil, "X"), &fakeModelRouter{}, "X", "m", 0)
		got := unroutable(nested)
		Expect(got).To(MatchError(cluster.ErrNoRoute))
		Expect(got).ToNot(MatchError(cluster.ErrNoConnection))
	})
})
