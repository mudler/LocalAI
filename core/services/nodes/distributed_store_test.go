package nodes

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

// --- fakeModelLookup ---

type fakeModelLookup struct {
	nodesByModel map[string]*BackendNode // modelName -> node
	allModels    []NodeModel
	allModelsErr error
	nodes        map[string]*BackendNode // nodeID -> node
}

func newFakeModelLookup() *fakeModelLookup {
	return &fakeModelLookup{
		nodesByModel: make(map[string]*BackendNode),
		nodes:        make(map[string]*BackendNode),
	}
}

func (f *fakeModelLookup) FindNodeForModel(_ context.Context, modelName string) (*BackendNode, bool) {
	n, ok := f.nodesByModel[modelName]
	return n, ok
}

func (f *fakeModelLookup) ListAllLoadedModels(_ context.Context) ([]NodeModel, error) {
	return f.allModels, f.allModelsErr
}

func (f *fakeModelLookup) Get(_ context.Context, nodeID string) (*BackendNode, error) {
	n, ok := f.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	return n, nil
}

// Compile-time interface check
var _ ModelLookup = (*fakeModelLookup)(nil)

var _ = Describe("DistributedModelStore", func() {
	var (
		local   *model.InMemoryModelStore
		lookup  *fakeModelLookup
		clients *fakeBackendClientFactory
		store   *DistributedModelStore
	)

	BeforeEach(func() {
		local = model.NewInMemoryModelStore()
		lookup = newFakeModelLookup()
		clients = newFakeBackendClientFactory()
		store = NewDistributedModelStore(local, lookup, clients)
	})

	Describe("Get", func() {
		It("returns from local cache on hit", func() {
			m := model.NewModel("my-model", "10.0.0.1:50051", nil)
			local.Set("my-model", m)

			got, ok := store.Get("my-model")
			Expect(ok).To(BeTrue())
			Expect(got).To(Equal(m))
		})

		It("does not fall back to DB — only returns locally-managed models", func() {
			node := &BackendNode{
				ID:      "node-1",
				Address: "10.0.0.2:50051",
			}
			lookup.nodesByModel["remote-model"] = node

			got, ok := store.Get("remote-model")
			Expect(ok).To(BeFalse())
			Expect(got).To(BeNil())
		})

		It("returns nil when not in DB either", func() {
			got, ok := store.Get("missing-model")
			Expect(ok).To(BeFalse())
			Expect(got).To(BeNil())
		})
	})

	Describe("Range", func() {
		It("iterates local and DB models, deduplicating", func() {
			// Local model
			localModel := model.NewModel("model-a", "10.0.0.1:50051", nil)
			local.Set("model-a", localModel)

			// DB model (not in local)
			dbNode := &BackendNode{ID: "node-2"}
			lookup.nodes["node-2"] = dbNode
			lookup.allModels = []NodeModel{
				{NodeID: "node-2", ModelName: "model-b", WorkerLocalAddress: "127.0.0.1:50052"},
				{NodeID: "node-2", ModelName: "model-a", WorkerLocalAddress: "127.0.0.1:50053"}, // duplicate, should be skipped
			}

			visited := make(map[string]bool)
			store.Range(func(id string, m *model.Model) bool {
				visited[id] = true
				return true
			})

			Expect(visited).To(HaveKey("model-a"))
			Expect(visited).To(HaveKey("model-b"))
			Expect(visited).To(HaveLen(2))
		})

		It("gives every remote model a client that reaches the worker through its node", func() {
			// The second construction path, closed. A model built with a nil
			// client makes pkg/model.Model.GRPC dial its raw address with
			// gRPC's own dialer the first time anything touches it, which
			// bypasses the worker's tunnel completely. It is reached in
			// production: ShutdownModel calls Free on it and the backend
			// monitor calls Status.
			dbNode := &BackendNode{ID: "node-2"}
			lookup.nodes["node-2"] = dbNode
			lookup.allModels = []NodeModel{{NodeID: "node-2", ModelName: "remote-model", WorkerLocalAddress: "127.0.0.1:50052"}}

			var got *model.Model
			store.Range(func(id string, m *model.Model) bool {
				if id == "remote-model" {
					got = m
				}
				return true
			})
			Expect(got).ToNot(BeNil())
			// The client is the factory's, so GRPC() returns it rather than
			// building one by dialling. Asked for by NODE, not by address.
			Expect(got.GRPC(false, nil)).To(BeIdenticalTo(grpc.Backend(clients.defaultClient)))
			Expect(clients.nodesSeen()).To(ContainElement("node-2"))
		})

		It("names the replica's own backend process, not the node", func() {
			// This used to pass the NODE's address, which was the worker's base
			// gRPC port and never the port a backend process listens on, so
			// Free and Status on a model listed here went to the wrong process.
			// A node has no address at all now, so the same code would name the
			// empty string and the worker would refuse the stream as invalid, a
			// refusal that reads as the backend answering about itself.
			lookup.nodes["node-2"] = &BackendNode{ID: "node-2"}
			lookup.allModels = []NodeModel{{
				NodeID: "node-2", ModelName: "remote-model", ReplicaIndex: 1,
				WorkerLocalAddress: "127.0.0.1:50057",
			}}

			store.Range(func(string, *model.Model) bool { return true })
			Expect(clients.addressesSeen()).To(ConsistOf("127.0.0.1:50057"))
		})

		It("skips a replica row that names no backend process", func() {
			// Nothing can be routed to it, and handing back a model whose
			// client targets an empty address turns every Free and Status on it
			// into an invalid-stream refusal from the worker.
			lookup.nodes["node-2"] = &BackendNode{ID: "node-2"}
			lookup.allModels = []NodeModel{{NodeID: "node-2", ModelName: "unnamed-model"}}

			visited := map[string]bool{}
			store.Range(func(id string, _ *model.Model) bool {
				visited[id] = true
				return true
			})
			Expect(visited).ToNot(HaveKey("unnamed-model"))
			Expect(clients.addressesSeen()).To(BeEmpty())
		})

		It("refuses to list a remote model it has no way to reach", func() {
			// Loudly, not by falling back. A model handed back here with a
			// direct-dialling client works on a single-host developer setup and
			// fails against every worker with no inbound port, which is the
			// worst way for this defect to behave.
			clients.refuseForNode = errors.New("no tunnel for you")
			dbNode := &BackendNode{ID: "node-2"}
			lookup.nodes["node-2"] = dbNode
			lookup.allModels = []NodeModel{{NodeID: "node-2", ModelName: "remote-model", WorkerLocalAddress: "127.0.0.1:50052"}}

			visited := map[string]bool{}
			store.Range(func(id string, _ *model.Model) bool {
				visited[id] = true
				return true
			})
			Expect(visited).ToNot(HaveKey("remote-model"))
		})

		It("refuses when no client factory was wired at all", func() {
			bare := NewDistributedModelStore(local, lookup, nil)
			dbNode := &BackendNode{ID: "node-2"}
			lookup.nodes["node-2"] = dbNode
			lookup.allModels = []NodeModel{{NodeID: "node-2", ModelName: "remote-model", WorkerLocalAddress: "127.0.0.1:50052"}}

			visited := map[string]bool{}
			bare.Range(func(id string, _ *model.Model) bool {
				visited[id] = true
				return true
			})
			Expect(visited).ToNot(HaveKey("remote-model"))
		})

		It("handles DB list error gracefully", func() {
			localModel := model.NewModel("model-x", "10.0.0.1:50051", nil)
			local.Set("model-x", localModel)

			lookup.allModelsErr = fmt.Errorf("db connection lost")

			visited := make(map[string]bool)
			store.Range(func(id string, m *model.Model) bool {
				visited[id] = true
				return true
			})

			// Should still have iterated local models
			Expect(visited).To(HaveKey("model-x"))
			Expect(visited).To(HaveLen(1))
		})
	})
})
