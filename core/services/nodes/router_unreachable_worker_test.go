// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// unreachableClientFactory cannot build a client for any node, standing in for
// a frontend whose worker tunnel dialer is missing or broken.
type unreachableClientFactory struct{}

func (unreachableClientFactory) NewClientForNode(_, _ string, _ bool) (grpc.Backend, error) {
	return nil, errors.New("no way to reach that worker")
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
		nm := &NodeModel{NodeID: "X", ModelName: "m", Address: "10.0.0.1:9001"}
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
