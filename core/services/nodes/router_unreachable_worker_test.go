// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"fmt"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// unreachableClientFactory cannot build a client for any node, standing in for
// a frontend whose worker tunnel dialer is missing or broken. This is the
// BOOT-TIME half of unroutability.
type unreachableClientFactory struct{}

func (unreachableClientFactory) NewClientForNode(_, _ string, _ bool) (grpc.Backend, error) {
	return nil, ErrNoWorkerDialer
}

// deadDialFactory builds clients normally and fails the DIAL, which is the
// RUNNING half and by far the likelier one: the factory only fails when the
// wiring is absent, while the dial fails whenever the replica holding a
// worker's tunnel is momentarily unreachable, which one frontend restart
// produces for every worker that replica holds.
func deadDialFactory(cause error) BackendClientFactory {
	GinkgoHelper()
	f, err := NewTunnelClientFactory("", func(string) func(ctx context.Context, addr string) (net.Conn, error) {
		return func(context.Context, string) (net.Conn, error) { return nil, cause }
	})
	Expect(err).ToNot(HaveOccurred())
	return f
}

var _ = Describe("routing when the worker cannot be reached at all", func() {
	// The catastrophe this phase exists to prevent, at the router. A frontend
	// that cannot reach a worker has learned NOTHING about that worker's
	// backends. Treating it as a failed health probe would reap the replica row
	// for every model in the deployment while those models carried on running,
	// and the reap is silent: the row is simply deleted and the model
	// cold-loaded somewhere else.
	loadedReg := func() *fakeModelRouter {
		node := &BackendNode{ID: "X", Name: "node-x", Address: "10.0.0.1:50051"}
		nm := &NodeModel{NodeID: "X", ModelName: "m", WorkerLocalAddress: "10.0.0.1:9001"}
		return &fakeModelRouter{
			findAndLockNode:          node,
			findAndLockNM:            nm,
			loadedReplicaStatsByName: map[string][]ReplicaCandidate{"m": {{NodeID: "X", InFlight: 0}}},
		}
	}

	It("never removes the replica row of a worker it merely cannot reach", func() {
		reg := loadedReg()
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      &fakeUnloader{},
			ClientFactory: unreachableClientFactory{},
		})

		_, err := router.Route(context.Background(), "m", "models/m.gguf", "llama-cpp", "", nil, false)
		// The request cannot be served, which is right and loud.
		Expect(err).To(HaveOccurred())
		// What must NOT have happened is the replica being reclaimed.
		Expect(reg.removeCalls).To(BeEmpty(),
			"a worker this frontend cannot reach must never have its loaded models reaped")
	})

	It("releases the routing reservation it took before giving up", func() {
		// FindAndLockNodeWithModel increments in_flight as a reservation. A
		// path that returns without releasing it leaves the replica looking
		// permanently busy, which is how a warm replica stops being picked at
		// all.
		reg := loadedReg()
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      &fakeUnloader{},
			ClientFactory: unreachableClientFactory{},
		})

		_, err := router.Route(context.Background(), "m", "models/m.gguf", "llama-cpp", "", nil, false)
		Expect(err).To(HaveOccurred())
		Expect(reg.decrementCalls).To(ContainElement("X:m"))
	})
})

var _ = Describe("routing when the worker's tunnel dial fails", func() {
	// The reviewer's spec. It is the boundary test: the factory succeeds, the
	// gRPC client is built, and the DIAL fails underneath with a
	// cluster.ErrNoRoute. gRPC flattens that into codes.Unavailable, which is
	// also what a dead backend produces, so without a way to carry the
	// distinction past the package boundary a peer link blip is read as a dead
	// process and the replica row is deleted after ONE miss.
	loadedReg := func() *fakeModelRouter {
		node := &BackendNode{ID: "X", Name: "node-x", Address: "10.0.0.1:50051"}
		nm := &NodeModel{NodeID: "X", ModelName: "m", WorkerLocalAddress: "10.0.0.1:9001"}
		return &fakeModelRouter{
			findAndLockNode:          node,
			findAndLockNM:            nm,
			loadedReplicaStatsByName: map[string][]ReplicaCandidate{"m": {{NodeID: "X", InFlight: 0}}},
		}
	}

	route := func(reg *fakeModelRouter, cause error) error {
		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      &fakeUnloader{},
			ClientFactory: deadDialFactory(cause),
		})
		_, err := router.Route(context.Background(), "m", "models/m.gguf", "llama-cpp", "", nil, false)
		return err
	}

	It("never reaps a replica whose OWNER replica is unreachable", func() {
		reg := loadedReg()
		Expect(route(reg, fmt.Errorf("through replica %q: %w: %w", "peer-2", cluster.ErrNoRoute, cluster.ErrPeerUnreachable))).To(HaveOccurred())
		Expect(reg.removeCalls).To(BeEmpty(),
			"a worker whose OWNER replica is unreachable must never have its loaded models reaped")
	})

	It("never reaps a replica that has not dialled its tunnel yet", func() {
		// The rolling-upgrade case end to end. A frontend-first upgrade puts
		// every not-yet-restarted worker here at once, and every one of them is
		// heartbeating and serving while it happens.
		reg := loadedReg()
		Expect(route(reg, fmt.Errorf("reaching node %q: %w", "X", cluster.ErrNoRoute))).To(HaveOccurred())
		Expect(reg.removeCalls).To(BeEmpty())
	})

	It("still releases the reservation it took", func() {
		reg := loadedReg()
		Expect(route(reg, fmt.Errorf("%w", cluster.ErrNoRoute))).To(HaveOccurred())
		Expect(reg.decrementCalls).To(ContainElement("X:m"))
	})

	It("carries the cluster condition all the way across the package boundary", func() {
		// Not just "something failed": the specific reason survives gRPC, which
		// is what makes the five conditions usable on this side. If this ever
		// reduces to a bare code, the consumers above are guessing again.
		f := deadDialFactory(fmt.Errorf("through replica %q: %w: %w", "peer-2", cluster.ErrNoRoute, cluster.ErrPeerUnreachable))
		client, err := f.NewClientForNode("X", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.HealthCheck(ctx)

		unreached := unroutable(client)
		Expect(unreached).To(MatchError(ErrWorkerUnroutable))
		Expect(unreached).To(MatchError(cluster.ErrNoRoute))
		Expect(unreached).To(MatchError(cluster.ErrPeerUnreachable))
		// And never absence, at either end of the trip.
		Expect(unreached).ToNot(MatchError(cluster.ErrNoConnection))
		Expect(unreached).ToNot(MatchError(cluster.ErrInstanceNotFound))
	})

	It("reports nothing for a client whose dial succeeded", func() {
		// The other direction, so the seam cannot pass by always saying yes: a
		// backend that genuinely died must still be reapable.
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
		client, err := f.NewClientForNode("X", "10.0.0.1:9001", false)
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The listener accepts and speaks no gRPC, so the RPC fails while the
		// DIAL succeeds. That is exactly a dead-ish backend on a reachable
		// worker, and it must not read as unroutable.
		_, _ = client.HealthCheck(ctx)
		Expect(unroutable(client)).To(BeNil())
	})
})
