package nodes

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
		local  *model.InMemoryModelStore
		lookup *fakeModelLookup
		store  *DistributedModelStore
	)

	BeforeEach(func() {
		local = model.NewInMemoryModelStore()
		lookup = newFakeModelLookup()
		store = NewDistributedModelStore(local, lookup)
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
			dbNode := &BackendNode{ID: "node-2", Address: "10.0.0.3:50051"}
			lookup.nodes["node-2"] = dbNode
			lookup.allModels = []NodeModel{
				{NodeID: "node-2", ModelName: "model-b"},
				{NodeID: "node-2", ModelName: "model-a"}, // duplicate — should be skipped
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
