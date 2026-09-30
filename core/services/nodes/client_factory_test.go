package nodes

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// recordingFactory records the node id and address of every client it builds,
// so a spec can assert that each consumer passes the node it is dialing.
type recordingFactory struct {
	mu       sync.Mutex
	seen     []string
	parallel []bool
	next     func() grpc.Backend
}

func (f *recordingFactory) NewClient(nodeID, address string, parallel bool) grpc.Backend {
	f.mu.Lock()
	f.seen = append(f.seen, nodeID+"@"+address)
	f.parallel = append(f.parallel, parallel)
	f.mu.Unlock()
	if f.next != nil {
		return f.next()
	}
	return nil
}

func (f *recordingFactory) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// parallelFlags returns the parallel argument of every build, in call order,
// so a spec can pin whether a consumer asks for a serialised client.
func (f *recordingFactory) parallelFlags() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.parallel...)
}

var _ = Describe("Backend client construction carries the node id", func() {
	It("hands the node id and the replica address to the factory from the health monitor", func() {
		f := &recordingFactory{next: func() grpc.Backend { return &fakeBackendClient{healthy: true} }}
		store := newFakeNodeHealthStore()
		hm := newTestHealthMonitor(store, f, true, 30*time.Second)
		hm.perModelHealthCheck = true

		store.addNode(makeTestNode("n1", "worker-1", "10.0.0.1:50051", StatusHealthy, freshTime()))
		store.addNodeModel("n1", NodeModel{NodeID: "n1", ModelName: "m", Address: "10.0.0.1:50052"})

		hm.doCheckAll(context.Background())

		Expect(f.calls()).To(Equal([]string{"n1@10.0.0.1:50052"}))
	})

	It("hands the node id and the replica address to the factory from the router", func() {
		f := &recordingFactory{next: func() grpc.Backend { return &stubBackend{healthResult: true} }}
		reg := &fakeModelRouter{
			findAndLockNode: &BackendNode{ID: "n1", Name: "node-1", Address: "10.0.0.2:50051"},
			findAndLockNM:   &NodeModel{NodeID: "n1", ModelName: "my-model", Address: "10.0.0.2:9001"},
		}
		router := NewSmartRouter(reg, SmartRouterOptions{ClientFactory: f})

		result, err := router.Route(context.Background(), "my-model", "models/my-model.gguf", "llama-cpp", "", nil, false)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(result.Release)

		// Route builds one client to probe the replica and one to serve it;
		// both dial the same node, so every build must carry its id.
		Expect(f.calls()).NotTo(BeEmpty())
		Expect(f.calls()).To(HaveEach("n1@10.0.0.2:9001"))
	})
})
