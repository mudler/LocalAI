package distributed_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Node Backend Lifecycle over the worker control plane", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_lifecycle_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("backend.install", func() {
		It("should send backend.install to a specific node", func() {
			node := &nodes.BackendNode{
				Name: "gpu-node-1", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// The worker serves backend.install on its own control plane.
			workers := NewControlWorkers()
			workers.On(node.ID, workerctl.PathBackendInstall, func(_ string, data []byte) any {
				var req messaging.BackendInstallRequest
				Expect(json.Unmarshal(data, &req)).To(Succeed())
				Expect(req.Backend).To(Equal("llama-cpp"))
				return messaging.BackendInstallReply{Success: true}
			})

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, workers.Client(), 3*time.Minute, 15*time.Minute)
			installReply, err := adapter.InstallBackend(node.ID, "llama-cpp", "", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(installReply.Success).To(BeTrue())
		})

		It("should propagate error from worker on failed install", func() {
			node := &nodes.BackendNode{
				Name: "fail-node", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// The worker's own verdict: an answer, not a transport failure.
			workers := NewControlWorkers()
			workers.On(node.ID, workerctl.PathBackendInstall, func(string, []byte) any {
				return messaging.BackendInstallReply{Success: false, Error: "backend not found"}
			})

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, workers.Client(), 3*time.Minute, 15*time.Minute)
			installReply, err := adapter.InstallBackend(node.ID, "nonexistent", "", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(installReply.Success).To(BeFalse())
			Expect(installReply.Error).To(ContainSubstring("backend not found"))
		})
	})

	Context("backend.stop (model unload)", func() {
		It("should send backend.stop to nodes hosting the model", func() {
			node := &nodes.BackendNode{
				Name: "gpu-node-2", Address: "h2:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "whisper-large", 0, "loaded", "", 0)).To(Succeed())

			var stopReceived atomic.Int32
			workers := NewControlWorkers()
			workers.On(node.ID, workerctl.PathBackendStop, func(string, []byte) any {
				stopReceived.Add(1)
				return nil
			})

			// Frontend calls UnloadRemoteModel (triggered by UI "Stop" or WatchDog)
			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, workers.Client(), 3*time.Minute, 15*time.Minute)
			Expect(adapter.UnloadRemoteModel("whisper-large")).To(Succeed())

			Eventually(func() int32 { return stopReceived.Load() }, "5s").Should(Equal(int32(1)))

			// Model should be removed from registry
			nodesWithModel, _ := registry.FindNodesWithModel(context.Background(), "whisper-large")
			Expect(nodesWithModel).To(BeEmpty())
		})

		It("should send backend.stop to all nodes hosting the model", func() {
			node1 := &nodes.BackendNode{Name: "n1", Address: "h1:50051"}
			node2 := &nodes.BackendNode{Name: "n2", Address: "h2:50051"}
			registry.Register(context.Background(), node1, true)
			registry.Register(context.Background(), node2, true)
			registry.SetNodeModel(context.Background(), node1.ID, "shared-model", 0, "loaded", "", 0)
			registry.SetNodeModel(context.Background(), node2.ID, "shared-model", 0, "loaded", "", 0)

			var count atomic.Int32
			workers := NewControlWorkers()
			for _, id := range []string{node1.ID, node2.ID} {
				workers.On(id, workerctl.PathBackendStop, func(string, []byte) any {
					count.Add(1)
					return nil
				})
			}

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, workers.Client(), 3*time.Minute, 15*time.Minute)
			adapter.UnloadRemoteModel("shared-model")

			Eventually(func() int32 { return count.Load() }, "5s").Should(Equal(int32(2)))
		})

		It("should be no-op for models not on any node", func() {
			// Unloading is idempotent by contract: cleanup paths (model
			// deletion, config edits, watchdog eviction) legitimately run
			// against an already-unloaded model, and the watchdog's LRU
			// reclaimer only untracks a model when shutdown reports success.
			// Callers needing to tell "stopped it" from "nothing to stop" —
			// ShutdownModel, so it can answer 404 — use HasRemoteModel instead.
			// The same contract is pinned at unit level by "with no nodes
			// returns nil" in core/services/nodes/unloader_test.go; keep them
			// in step.
			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, NewControlWorkers().Client(), 3*time.Minute, 15*time.Minute)
			Expect(adapter.UnloadRemoteModel("nonexistent-model")).To(Succeed())
		})
	})

	Context("node.stop (full shutdown)", func() {
		It("should ask the node to shut down over its control plane", func() {
			node := &nodes.BackendNode{
				Name: "stop-me", Address: "h3:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			var stopped atomic.Int32
			workers := NewControlWorkers()
			workers.On(node.ID, workerctl.PathNodeStop, func(string, []byte) any {
				stopped.Add(1)
				return nil
			})

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, workers.Client(), 3*time.Minute, 15*time.Minute)
			Expect(adapter.StopNode(node.ID)).To(Succeed())

			Eventually(func() int32 { return stopped.Load() }, "5s").Should(Equal(int32(1)))
		})
	})

	Context("wire naming", func() {
		// Written out BY HAND, and not derived from the constants: a frontend
		// and a worker built from different commits reach each other over these
		// literals, and a renamed path is a 404 that looks exactly like a
		// broken tunnel.
		It("should name the backend lifecycle control verbs", func() {
			Expect(workerctl.PathBackendInstall).To(Equal("/v1/control/backend/install"))
			Expect(workerctl.PathBackendStop).To(Equal("/v1/control/backend/stop"))
			Expect(workerctl.PathNodeStop).To(Equal("/v1/control/node/stop"))
		})

		// The one node subject left, and it is addressed only to AGENT workers:
		// they hold no tunnel and subscribe to it to drop cached MCP sessions.
		It("should keep the agent worker's backend.stop subject", func() {
			Expect(messaging.SubjectNodeBackendStop("node-abc")).To(Equal("nodes.node-abc.backend.stop"))
		})
	})

	// Design note: LoadModel is a gRPC call through the worker's tunnel, not a
	// control verb. The control plane installs and stops the process; the model
	// is loaded into it over the `grpc` stream tag.
	//
	// Flow:
	// 1. backend.install → worker installs backend + starts gRPC process
	// 2. SmartRouter.Route() → LoadModel over the worker's tunnel
	// 3. [inference over the tunnel]
	// 4. backend.stop → worker stops gRPC process
})
