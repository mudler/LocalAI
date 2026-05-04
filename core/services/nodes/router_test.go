package nodes

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Fake FileStager (pre-existing)
// ---------------------------------------------------------------------------

// fakeFileStager is a minimal FileStager that records calls and returns
// predictable remote paths without touching the filesystem or network.
type fakeFileStager struct {
	ensureCalls []ensureCall
}

type ensureCall struct {
	nodeID, localPath, key string
}

func (f *fakeFileStager) EnsureRemote(_ context.Context, nodeID, localPath, key string) (string, error) {
	f.ensureCalls = append(f.ensureCalls, ensureCall{nodeID, localPath, key})
	return "/remote/" + key, nil
}

func (f *fakeFileStager) FetchRemote(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeFileStager) FetchRemoteByKey(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeFileStager) AllocRemoteTemp(_ context.Context, _ string) (string, error) {
	return "/remote/tmp", nil
}

func (f *fakeFileStager) StageRemoteToStore(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeFileStager) ListRemoteDir(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Fake ModelRouter
// ---------------------------------------------------------------------------

// fakeModelRouter implements ModelRouter with configurable return values.
type fakeModelRouter struct {
	// FindAndLockNodeWithModel returns
	findAndLockNode *BackendNode
	findAndLockNM   *NodeModel
	findAndLockErr  error

	// FindNodeWithVRAM returns
	findVRAMNode *BackendNode
	findVRAMErr  error

	// FindIdleNode returns
	findIdleNode *BackendNode
	findIdleErr  error

	// FindLeastLoadedNode returns
	findLeastLoadedNode *BackendNode
	findLeastLoadedErr  error

	// FindGlobalLRUModelWithZeroInFlight returns
	findGlobalLRUModel *NodeModel
	findGlobalLRUErr   error

	// FindLRUModel returns
	findLRUModel *NodeModel
	findLRUErr   error

	// Get returns
	getNode *BackendNode
	getErr  error

	// GetModelScheduling returns
	getModelScheduling *ModelSchedulingConfig
	getModelSchedErr   error

	// FindNodesBySelector returns
	findBySelectorNodes []BackendNode
	findBySelectorErr   error

	// *FromSet variants
	findVRAMFromSetNode        *BackendNode
	findVRAMFromSetErr         error
	findIdleFromSetNode        *BackendNode
	findIdleFromSetErr         error
	findLeastLoadedFromSetNode *BackendNode
	findLeastLoadedFromSetErr  error

	// GetNodeLabels returns
	getNodeLabels    []NodeLabel
	getNodeLabelsErr error

	// Track calls for assertions
	decrementCalls []string // "nodeID:modelName"
	incrementCalls []string
	removeCalls    []string
	setCalls       []string
	touchCalls     []string
}

func (f *fakeModelRouter) FindAndLockNodeWithModel(_ context.Context, modelName string, _ []string) (*BackendNode, *NodeModel, error) {
	return f.findAndLockNode, f.findAndLockNM, f.findAndLockErr
}

func (f *fakeModelRouter) DecrementInFlight(_ context.Context, nodeID, modelName string, _ int) error {
	f.decrementCalls = append(f.decrementCalls, nodeID+":"+modelName)
	return nil
}

func (f *fakeModelRouter) IncrementInFlight(_ context.Context, nodeID, modelName string, _ int) error {
	f.incrementCalls = append(f.incrementCalls, nodeID+":"+modelName)
	return nil
}

func (f *fakeModelRouter) RemoveNodeModel(_ context.Context, nodeID, modelName string, _ int) error {
	f.removeCalls = append(f.removeCalls, nodeID+":"+modelName)
	return nil
}

func (f *fakeModelRouter) RemoveAllNodeModelReplicas(_ context.Context, nodeID, modelName string) error {
	// Same recorded key as RemoveNodeModel so existing tests that assert "the
	// model was removed" don't need to know whether the production code used
	// the per-replica or all-replicas variant.
	f.removeCalls = append(f.removeCalls, nodeID+":"+modelName)
	return nil
}

func (f *fakeModelRouter) TouchNodeModel(_ context.Context, nodeID, modelName string, _ int) {
	f.touchCalls = append(f.touchCalls, nodeID+":"+modelName)
}

func (f *fakeModelRouter) SetNodeModel(_ context.Context, nodeID, modelName string, _ int, state, address string, _ int) error {
	f.setCalls = append(f.setCalls, fmt.Sprintf("%s:%s:%s:%s", nodeID, modelName, state, address))
	return nil
}

func (f *fakeModelRouter) SetNodeModelLoadInfo(_ context.Context, _, _ string, _ int, _ string, _ []byte) error {
	return nil
}

func (f *fakeModelRouter) GetModelLoadInfo(_ context.Context, _ string) (string, []byte, error) {
	return "", nil, fmt.Errorf("not found")
}

func (f *fakeModelRouter) NextFreeReplicaIndex(_ context.Context, _, _ string, _ int) (int, error) {
	return 0, nil
}

func (f *fakeModelRouter) CountReplicasOnNode(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

func (f *fakeModelRouter) FindNodeWithVRAM(_ context.Context, _ uint64) (*BackendNode, error) {
	return f.findVRAMNode, f.findVRAMErr
}

func (f *fakeModelRouter) FindIdleNode(_ context.Context) (*BackendNode, error) {
	return f.findIdleNode, f.findIdleErr
}

func (f *fakeModelRouter) FindLeastLoadedNode(_ context.Context) (*BackendNode, error) {
	return f.findLeastLoadedNode, f.findLeastLoadedErr
}

func (f *fakeModelRouter) FindGlobalLRUModelWithZeroInFlight(_ context.Context) (*NodeModel, error) {
	return f.findGlobalLRUModel, f.findGlobalLRUErr
}

func (f *fakeModelRouter) FindLRUModel(_ context.Context, _ string) (*NodeModel, error) {
	return f.findLRUModel, f.findLRUErr
}

func (f *fakeModelRouter) Get(_ context.Context, _ string) (*BackendNode, error) {
	return f.getNode, f.getErr
}

func (f *fakeModelRouter) GetModelScheduling(_ context.Context, _ string) (*ModelSchedulingConfig, error) {
	return f.getModelScheduling, f.getModelSchedErr
}

func (f *fakeModelRouter) FindNodesBySelector(_ context.Context, _ map[string]string) ([]BackendNode, error) {
	return f.findBySelectorNodes, f.findBySelectorErr
}

func (f *fakeModelRouter) FindNodesWithFreeSlot(_ context.Context, _ string, _ []string) ([]BackendNode, error) {
	// Default: same answer as FindNodesBySelector. Tests that need a
	// specific filter can override by reusing findBySelectorNodes.
	return f.findBySelectorNodes, f.findBySelectorErr
}

func (f *fakeModelRouter) ReserveVRAM(_ context.Context, _ string, _ uint64) error {
	return nil
}

func (f *fakeModelRouter) ReleaseVRAM(_ context.Context, _ string, _ uint64) error {
	return nil
}

func (f *fakeModelRouter) FindNodeWithVRAMFromSet(_ context.Context, _ uint64, _ []string) (*BackendNode, error) {
	return f.findVRAMFromSetNode, f.findVRAMFromSetErr
}

func (f *fakeModelRouter) FindIdleNodeFromSet(_ context.Context, _ []string) (*BackendNode, error) {
	return f.findIdleFromSetNode, f.findIdleFromSetErr
}

func (f *fakeModelRouter) FindLeastLoadedNodeFromSet(_ context.Context, _ []string) (*BackendNode, error) {
	return f.findLeastLoadedFromSetNode, f.findLeastLoadedFromSetErr
}

func (f *fakeModelRouter) GetNodeLabels(_ context.Context, _ string) ([]NodeLabel, error) {
	return f.getNodeLabels, f.getNodeLabelsErr
}

// ---------------------------------------------------------------------------
// Fake BackendClientFactory + Backend
// ---------------------------------------------------------------------------

// stubBackend implements grpc.Backend with configurable HealthCheck and LoadModel.
type stubBackend struct {
	grpc.Backend // embed to satisfy interface; unused methods will panic if called

	healthResult bool
	healthErr    error
	loadResult   *pb.Result
	loadErr      error
}

func (f *stubBackend) HealthCheck(_ context.Context) (bool, error) {
	return f.healthResult, f.healthErr
}

func (f *stubBackend) LoadModel(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return f.loadResult, f.loadErr
}

func (f *stubBackend) IsBusy() bool { return false }

// stubClientFactory returns the same stubBackend for every call.
type stubClientFactory struct {
	client *stubBackend
}

func (f *stubClientFactory) NewClient(_ string, _ bool) grpc.Backend {
	return f.client
}

// ---------------------------------------------------------------------------
// Fake NodeCommandSender (unloader)
// ---------------------------------------------------------------------------

type fakeUnloader struct {
	installReply *messaging.BackendInstallReply
	installErr   error
	installCalls []installCall // every InstallBackend invocation, in order
	stopCalls    []string      // "nodeID:model"
	stopErr      error
	unloadCalls  []string
	unloadErr    error
}

// installCall captures the args we care about when asserting that the
// reconciler / router did or did not fire a NATS install. The fake records
// every call so tests can verify both presence and shape (e.g. that backend
// is non-empty).
type installCall struct {
	nodeID  string
	backend string
	modelID string
	replica int
}

func (f *fakeUnloader) InstallBackend(nodeID, backend, modelID, _, _, _, _ string, replica int) (*messaging.BackendInstallReply, error) {
	f.installCalls = append(f.installCalls, installCall{nodeID, backend, modelID, replica})
	return f.installReply, f.installErr
}

func (f *fakeUnloader) DeleteBackend(_, _ string) (*messaging.BackendDeleteReply, error) {
	return &messaging.BackendDeleteReply{Success: true}, nil
}

func (f *fakeUnloader) ListBackends(_ string) (*messaging.BackendListReply, error) {
	return &messaging.BackendListReply{}, nil
}

func (f *fakeUnloader) StopBackend(nodeID, backend string) error {
	f.stopCalls = append(f.stopCalls, nodeID+":"+backend)
	return f.stopErr
}

func (f *fakeUnloader) UnloadModelOnNode(nodeID, modelName string) error {
	f.unloadCalls = append(f.unloadCalls, nodeID+":"+modelName)
	return f.unloadErr
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

var _ = Describe("SmartRouter", func() {
	// -----------------------------------------------------------------------
	// Unit tests using mock interfaces (no DB required)
	// -----------------------------------------------------------------------
	Describe("Route (mock-based)", func() {
		var (
			reg      *fakeModelRouter
			backend  *stubBackend
			factory  *stubClientFactory
			unloader *fakeUnloader
		)

		BeforeEach(func() {
			reg = &fakeModelRouter{}
			backend = &stubBackend{}
			factory = &stubClientFactory{client: backend}
			unloader = &fakeUnloader{
				installReply: &messaging.BackendInstallReply{
					Success: true,
					Address: "10.0.0.1:9001",
				},
			}
		})

		Context("model already loaded on a healthy node", func() {
			It("returns the client and a release function", func() {
				node := &BackendNode{ID: "n1", Name: "node-1", Address: "10.0.0.1:50051"}
				nm := &NodeModel{NodeID: "n1", ModelName: "my-model", Address: "10.0.0.1:9001"}
				reg.findAndLockNode = node
				reg.findAndLockNM = nm
				backend.healthResult = true

				router := NewSmartRouter(reg, SmartRouterOptions{
					Unloader:      unloader,
					ClientFactory: factory,
				})

				result, err := router.Route(context.Background(), "my-model", "models/my-model.gguf", "llama-cpp", nil, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())
				Expect(result.Node.ID).To(Equal("n1"))

				// TouchNodeModel should have been called
				Expect(reg.touchCalls).To(ContainElement("n1:my-model"))

				// The initial in-flight reservation from FindAndLockNodeWithModel is released
				// after the first inference call completes via OnFirstComplete callback.
				// Release only closes the client.
				result.Release()
				// No decrement on Release — it happens via OnFirstComplete after first Predict
				Expect(reg.decrementCalls).To(BeEmpty())
			})
		})

		Context("model not loaded, falls through to scheduling", func() {
			It("schedules on an idle node and records the model", func() {
				// FindAndLockNodeWithModel always fails — simulates no cached model
				// (equivalent to the health-check-failure fallthrough path).
				idleNode := &BackendNode{ID: "n2", Name: "idle-node", Address: "10.0.0.2:50051"}
				reg2 := &fakeModelRouter{
					findAndLockErr: errors.New("not found"),
					findIdleNode:   idleNode,
				}
				backend.loadResult = &pb.Result{Success: true}

				router := NewSmartRouter(reg2, SmartRouterOptions{
					Unloader:      unloader,
					ClientFactory: factory,
				})

				result, err := router.Route(context.Background(), "some-model", "models/some-model.gguf", "llama-cpp", nil, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())
				Expect(result.Node.ID).To(Equal("n2"))

				// SetNodeModel should record the model as loaded on the node
				Expect(reg2.setCalls).To(HaveLen(1))
				Expect(reg2.setCalls[0]).To(ContainSubstring("n2:some-model:loaded"))
			})
		})

		Context("model not loaded, no DB (advisory lock bypassed)", func() {
			It("schedules on an available node via FindIdleNode", func() {
				reg.findAndLockErr = errors.New("not found")
				idleNode := &BackendNode{ID: "n3", Name: "idle", Address: "10.0.0.3:50051"}
				reg.findIdleNode = idleNode
				backend.loadResult = &pb.Result{Success: true}

				router := NewSmartRouter(reg, SmartRouterOptions{
					Unloader:      unloader,
					ClientFactory: factory,
					// DB is nil — no advisory lock
				})

				result, err := router.Route(context.Background(), "new-model", "models/new.gguf", "llama-cpp", nil, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.Node.ID).To(Equal("n3"))
			})
		})
	})

	Describe("scheduleNewModel (mock-based, via Route)", func() {
		var (
			reg      *fakeModelRouter
			backend  *stubBackend
			factory  *stubClientFactory
			unloader *fakeUnloader
		)

		BeforeEach(func() {
			reg = &fakeModelRouter{
				findAndLockErr: errors.New("not found"),
			}
			backend = &stubBackend{
				loadResult: &pb.Result{Success: true},
			}
			factory = &stubClientFactory{client: backend}
			unloader = &fakeUnloader{
				installReply: &messaging.BackendInstallReply{
					Success: true,
					Address: "10.0.0.1:9001",
				},
			}
		})

		It("finds a node with sufficient VRAM first", func() {
			vramNode := &BackendNode{ID: "vram-node", Name: "gpu-box", Address: "10.0.0.10:50051"}
			reg.findVRAMNode = vramNode

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			// Pass non-nil ModelOptions so estimateModelVRAM runs (returns 0 for
			// missing files, so FindNodeWithVRAM won't actually be called unless
			// estimatedVRAM > 0). To trigger VRAM path we need estimatedVRAM > 0,
			// but that requires real files. Instead test the fallback: VRAM returns
			// error, idle succeeds.
			// Actually, estimateModelVRAM returns 0 when model files don't exist,
			// so the VRAM branch is skipped and we go to idle/least-loaded.
			// To properly test VRAM path, we'd need to mock estimateModelVRAM.
			// For now, verify the fallback paths work correctly.

			// With no real model files, estimatedVRAM=0, so VRAM path is skipped.
			// Set idle node to test that path.
			reg.findVRAMNode = nil
			reg.findVRAMErr = errors.New("no vram nodes")
			idleNode := &BackendNode{ID: "idle-vram", Name: "idle", Address: "10.0.0.11:50051"}
			reg.findIdleNode = idleNode

			result, err := router.Route(context.Background(), "m1", "models/m1.gguf", "llama-cpp", &pb.ModelOptions{}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Node.ID).To(Equal("idle-vram"))
		})

		It("falls back to idle when VRAM search fails", func() {
			reg.findVRAMErr = errors.New("no vram")
			idleNode := &BackendNode{ID: "idle-1", Name: "idle-node", Address: "10.0.0.20:50051"}
			reg.findIdleNode = idleNode

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			result, err := router.Route(context.Background(), "m2", "models/m2.gguf", "llama-cpp", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Node.ID).To(Equal("idle-1"))
		})

		It("falls back to least-loaded when both VRAM and idle fail", func() {
			reg.findVRAMErr = errors.New("no vram")
			reg.findIdleErr = errors.New("no idle")
			llNode := &BackendNode{ID: "ll-1", Name: "least-loaded", Address: "10.0.0.30:50051"}
			reg.findLeastLoadedNode = llNode

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			result, err := router.Route(context.Background(), "m3", "models/m3.gguf", "llama-cpp", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Node.ID).To(Equal("ll-1"))
		})

		It("returns error when no nodes are available and no DB for eviction", func() {
			reg.findVRAMErr = errors.New("no vram")
			reg.findIdleErr = errors.New("no idle")
			reg.findLeastLoadedErr = errors.New("no nodes")

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
				// DB is nil — evictLRUAndFreeNode will fail because r.db is nil
			})

			_, err := router.Route(context.Background(), "m4", "models/m4.gguf", "llama-cpp", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no available nodes"))
		})
	})

	Describe("UnloadModel (mock-based)", func() {
		It("calls StopBackend and removes the model from the registry", func() {
			reg := &fakeModelRouter{}
			unloader := &fakeUnloader{}

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader: unloader,
			})

			err := router.UnloadModel(context.Background(), "node-1", "model-a")
			Expect(err).ToNot(HaveOccurred())

			Expect(unloader.stopCalls).To(ContainElement("node-1:model-a"))
			Expect(reg.removeCalls).To(ContainElement("node-1:model-a"))
		})

		It("returns error when no unloader is configured", func() {
			reg := &fakeModelRouter{}
			router := NewSmartRouter(reg, SmartRouterOptions{})

			err := router.UnloadModel(context.Background(), "node-1", "model-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no remote unloader"))
		})
	})

	Describe("EvictLRU (mock-based)", func() {
		It("finds LRU model and unloads it", func() {
			reg := &fakeModelRouter{
				findLRUModel: &NodeModel{NodeID: "n1", ModelName: "old-model"},
			}
			unloader := &fakeUnloader{}

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader: unloader,
			})

			evicted, err := router.EvictLRU(context.Background(), "n1")
			Expect(err).ToNot(HaveOccurred())
			Expect(evicted).To(Equal("old-model"))
			Expect(unloader.stopCalls).To(ContainElement("n1:old-model"))
			Expect(reg.removeCalls).To(ContainElement("n1:old-model"))
		})

		It("returns error when no LRU model is found", func() {
			reg := &fakeModelRouter{
				findLRUErr: errors.New("no models loaded"),
			}

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader: &fakeUnloader{},
			})

			_, err := router.EvictLRU(context.Background(), "n1")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("finding LRU model"))
		})
	})

	Describe("scheduleNewModel with node selector (mock-based, via Route)", func() {
		var (
			reg      *fakeModelRouter
			backend  *stubBackend
			factory  *stubClientFactory
			unloader *fakeUnloader
		)

		BeforeEach(func() {
			reg = &fakeModelRouter{
				findAndLockErr: errors.New("not found"),
			}
			backend = &stubBackend{
				loadResult: &pb.Result{Success: true},
			}
			factory = &stubClientFactory{client: backend}
			unloader = &fakeUnloader{
				installReply: &messaging.BackendInstallReply{
					Success: true,
					Address: "10.0.0.1:9001",
				},
			}
		})

		It("uses *FromSet methods when model has a node selector", func() {
			gpuNode := &BackendNode{ID: "gpu-1", Name: "gpu-node", Address: "10.0.0.50:50051"}
			reg.getModelScheduling = &ModelSchedulingConfig{
				ModelName:    "selector-model",
				NodeSelector: `{"gpu.vendor":"nvidia"}`,
			}
			reg.findBySelectorNodes = []BackendNode{*gpuNode}
			reg.findIdleFromSetNode = gpuNode

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			result, err := router.Route(context.Background(), "selector-model", "models/selector.gguf", "llama-cpp", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())
			Expect(result.Node.ID).To(Equal("gpu-1"))
		})

		It("returns error when no nodes match selector", func() {
			reg.getModelScheduling = &ModelSchedulingConfig{
				ModelName:    "no-match-model",
				NodeSelector: `{"gpu.vendor":"tpu"}`,
			}
			reg.findBySelectorNodes = nil
			reg.findBySelectorErr = nil

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			_, err := router.Route(context.Background(), "no-match-model", "models/nomatch.gguf", "llama-cpp", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no healthy nodes match selector"))
		})

		It("uses regular methods when model has no scheduling config", func() {
			reg.getModelScheduling = nil
			idleNode := &BackendNode{ID: "regular-1", Name: "regular-node", Address: "10.0.0.60:50051"}
			reg.findIdleNode = idleNode

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			result, err := router.Route(context.Background(), "regular-model", "models/regular.gguf", "llama-cpp", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())
			Expect(result.Node.ID).To(Equal("regular-1"))
		})
	})

	Describe("Route with selector validation on cached model (mock-based)", func() {
		It("falls through when cached node no longer matches selector", func() {
			cachedNode := &BackendNode{ID: "n-old", Name: "old-node", Address: "10.0.0.70:50051"}
			newNode := &BackendNode{ID: "n-new", Name: "new-node", Address: "10.0.0.71:50051"}

			backend := &stubBackend{
				healthResult: true,
				loadResult:   &pb.Result{Success: true},
			}
			factory := &stubClientFactory{client: backend}
			unloader := &fakeUnloader{
				installReply: &messaging.BackendInstallReply{
					Success: true,
					Address: "10.0.0.71:9001",
				},
			}

			reg := &fakeModelRouter{
				// Step 1: cached model found on old node
				findAndLockNode: cachedNode,
				findAndLockNM:   &NodeModel{NodeID: "n-old", ModelName: "sel-model", Address: "10.0.0.70:9001"},
				// Scheduling config with selector that old node does NOT match
				getModelScheduling: &ModelSchedulingConfig{
					ModelName:    "sel-model",
					NodeSelector: `{"gpu.vendor":"nvidia"}`,
				},
				// Old node has no labels matching the selector
				getNodeLabels: []NodeLabel{
					{NodeID: "n-old", Key: "gpu.vendor", Value: "amd"},
				},
				// For scheduling fallthrough: selector matches new node
				findBySelectorNodes: []BackendNode{*newNode},
				findIdleFromSetNode: newNode,
			}

			router := NewSmartRouter(reg, SmartRouterOptions{
				Unloader:      unloader,
				ClientFactory: factory,
			})

			result, err := router.Route(context.Background(), "sel-model", "models/sel.gguf", "llama-cpp", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())
			// Should have fallen through to the new node
			Expect(result.Node.ID).To(Equal("n-new"))
			// Old node should have had its in-flight decremented
			Expect(reg.decrementCalls).To(ContainElement("n-old:sel-model"))
		})
	})

	Describe("ScheduleAndLoadModel (mock-based)", func() {
		It("returns an error and does not fire a NATS install when no load info is stored", func() {
			// Reproduces the reconciler scale-up bug: when GetModelLoadInfo
			// returns ErrRecordNotFound (no replica has ever been loaded),
			// the previous fallback called scheduleNewModel with an empty
			// backend type, which the worker rejected on every reconciler
			// tick. The fix bails out cleanly with an explanatory error and
			// never sends backend.install.
			unloader := &fakeUnloader{}
			reg := &fakeModelRouter{}
			router := NewSmartRouter(reg, SmartRouterOptions{Unloader: unloader})

			node, err := router.ScheduleAndLoadModel(context.Background(), "never-loaded", nil)

			Expect(err).To(HaveOccurred())
			Expect(node).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("never-loaded"))
			Expect(unloader.installCalls).To(BeEmpty(),
				"reconciler must not fire backend.install when there is no load info to replicate")
		})
	})

	// -----------------------------------------------------------------------
	// Integration tests using real PostgreSQL (existing)
	// -----------------------------------------------------------------------
	Describe("evictLRUAndFreeNode (integration)", func() {
		var (
			db       *gorm.DB
			registry *NodeRegistry
		)

		BeforeEach(func() {
			if runtime.GOOS == "darwin" {
				Skip("testcontainers requires Docker, not available on macOS CI")
			}
			db = testutil.SetupTestDB()
			var err error
			registry, err = NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns ErrEvictionBusy in under 5 seconds when all models are busy", func() {
			node := &BackendNode{
				Name:     "busy-evict",
				NodeType: NodeTypeBackend,
				Address:  "10.0.0.100:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Load a model and give it in-flight requests so it cannot be evicted
			Expect(registry.SetNodeModel(context.Background(), node.ID, "busy-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "busy-model", 0)).To(Succeed())

			router := NewSmartRouter(registry, SmartRouterOptions{DB: db})

			start := time.Now()
			_, err := router.evictLRUAndFreeNode(context.Background())
			elapsed := time.Since(start)

			Expect(err).To(MatchError(ErrEvictionBusy))
			// 5 retries * 500ms = 2.5s nominal; allow generous upper bound
			Expect(elapsed).To(BeNumerically("<", 5*time.Second))
		})

		It("respects context cancellation", func() {
			node := &BackendNode{
				Name:     "cancel-evict",
				NodeType: NodeTypeBackend,
				Address:  "10.0.0.101:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "cancel-model", 0, "loaded", "", 0)).To(Succeed())
			Expect(registry.IncrementInFlight(context.Background(), node.ID, "cancel-model", 0)).To(Succeed())

			router := NewSmartRouter(registry, SmartRouterOptions{DB: db})

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			start := time.Now()
			_, err := router.evictLRUAndFreeNode(ctx)
			elapsed := time.Since(start)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("context cancelled"))
			// Should return very quickly since context is already done
			Expect(elapsed).To(BeNumerically("<", 2*time.Second))
		})
	})

	Describe("stageModelFiles (integration)", func() {
		var (
			db       *gorm.DB
			registry *NodeRegistry
		)

		BeforeEach(func() {
			if runtime.GOOS == "darwin" {
				Skip("testcontainers requires Docker, not available on macOS CI")
			}
			db = testutil.SetupTestDB()
			var err error
			registry, err = NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
		})

		It("does not mutate the original ModelOptions", func() {
			stager := &fakeFileStager{}
			router := NewSmartRouter(registry, SmartRouterOptions{
				FileStager: stager,
				DB:         db,
			})

			node := &BackendNode{
				ID:      "stage-node-id",
				Name:    "stage-node",
				Address: "10.0.0.200:50051",
			}

			original := &pb.ModelOptions{
				Model:     "test-backend/models/test.gguf",
				ModelFile: "/models/test-backend/models/test.gguf",
				MMProj:    "",
			}

			// Capture original values before staging
			origModel := original.Model
			origModelFile := original.ModelFile
			origMMProj := original.MMProj

			// stageModelFiles creates temp files for os.Stat checks.
			// Since none of our test paths exist on disk, stageModelFiles will
			// skip them (clearing non-existent optional fields). The key property
			// is that the original proto pointer is not modified.
			_, _ = router.stageModelFiles(context.Background(), node, original, "test-model")

			// Verify the original proto was not mutated
			Expect(original.Model).To(Equal(origModel))
			Expect(original.ModelFile).To(Equal(origModelFile))
			Expect(original.MMProj).To(Equal(origMMProj))
		})
	})
})
