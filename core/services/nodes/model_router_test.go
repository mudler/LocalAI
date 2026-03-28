package nodes

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// --- fakeModelRouterForSmartRouter implements ModelRouter ---

type fakeModelRouterForSmartRouter struct {
	mu              sync.Mutex
	node            *BackendNode
	nodeModel       *NodeModel
	findErr         error
	decrementCalled map[string]int // "nodeID:model" -> count
}

func newFakeModelRouterForSmartRouter() *fakeModelRouterForSmartRouter {
	return &fakeModelRouterForSmartRouter{
		decrementCalled: make(map[string]int),
	}
}

func (f *fakeModelRouterForSmartRouter) FindAndLockNodeWithModel(_ context.Context, _ string) (*BackendNode, *NodeModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.node, f.nodeModel, f.findErr
}

func (f *fakeModelRouterForSmartRouter) DecrementInFlight(_ context.Context, nodeID, modelName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decrementCalled[nodeID+":"+modelName]++
	return nil
}

func (f *fakeModelRouterForSmartRouter) IncrementInFlight(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeModelRouterForSmartRouter) RemoveNodeModel(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeModelRouterForSmartRouter) TouchNodeModel(_ context.Context, _, _ string) {}
func (f *fakeModelRouterForSmartRouter) SetNodeModel(_ context.Context, _, _, _ string, _ ...string) error {
	return nil
}
func (f *fakeModelRouterForSmartRouter) FindNodeWithVRAM(_ context.Context, _ uint64) (*BackendNode, error) {
	return nil, nil
}
func (f *fakeModelRouterForSmartRouter) FindIdleNode(_ context.Context) (*BackendNode, error) {
	return nil, nil
}
func (f *fakeModelRouterForSmartRouter) FindLeastLoadedNode(_ context.Context) (*BackendNode, error) {
	return nil, nil
}
func (f *fakeModelRouterForSmartRouter) FindGlobalLRUModelWithZeroInFlight(_ context.Context) (*NodeModel, error) {
	return nil, nil
}
func (f *fakeModelRouterForSmartRouter) FindLRUModel(_ context.Context, _ string) (*NodeModel, error) {
	return nil, nil
}
func (f *fakeModelRouterForSmartRouter) Get(_ context.Context, nodeID string) (*BackendNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.node != nil && f.node.ID == nodeID {
		return f.node, nil
	}
	return nil, nil
}

// Compile-time check
var _ ModelRouter = (*fakeModelRouterForSmartRouter)(nil)

var _ = Describe("ModelRouterAdapter", func() {
	Describe("ReleaseModel", func() {
		It("calls stored release function", func() {
			adapter := &ModelRouterAdapter{
				release: make(map[string]func()),
			}

			released := false
			adapter.release["my-model"] = func() { released = true }

			adapter.ReleaseModel("my-model")

			Expect(released).To(BeTrue())
			// Release function should be removed after calling
			Expect(adapter.release).NotTo(HaveKey("my-model"))
		})

		It("is no-op for unknown model", func() {
			adapter := &ModelRouterAdapter{
				release: make(map[string]func()),
			}

			// Should not panic
			adapter.ReleaseModel("nonexistent-model")
			Expect(adapter.release).To(BeEmpty())
		})
	})

	Describe("Route", func() {
		It("delegates to SmartRouter and stores release func", func() {
			fakeNode := &BackendNode{
				ID:      "node-1",
				Name:    "test-node",
				Address: "10.0.0.1:50051",
			}
			fakeNM := &NodeModel{
				NodeID:    "node-1",
				ModelName: "test-model",
			}

			fakeReg := newFakeModelRouterForSmartRouter()
			fakeReg.node = fakeNode
			fakeReg.nodeModel = fakeNM

			// The fake gRPC client that SmartRouter will use for health check
			factory := newFakeBackendClientFactory()
			factory.setClient("10.0.0.1:50051", &fakeBackendClient{healthy: true})

			sr := NewSmartRouter(fakeReg, SmartRouterOptions{
				ClientFactory: factory,
			})
			adapter := NewModelRouterAdapter(sr)

			opts := &pb.ModelOptions{Model: "test-model"}
			m, err := adapter.Route(context.Background(), "llama-cpp", "test-model", "test-model", "model.gguf", opts, false)

			Expect(err).NotTo(HaveOccurred())
			Expect(m).NotTo(BeNil())
			Expect(m.ID).To(Equal("test-model"))

			// Verify release function was stored
			adapter.mu.Lock()
			_, hasRelease := adapter.release["test-model"]
			adapter.mu.Unlock()
			Expect(hasRelease).To(BeTrue())

			// Verify calling ReleaseModel triggers the release (which decrements in-flight)
			adapter.ReleaseModel("test-model")

			fakeReg.mu.Lock()
			count := fakeReg.decrementCalled["node-1:test-model"]
			fakeReg.mu.Unlock()
			Expect(count).To(Equal(1))
		})
	})
})
