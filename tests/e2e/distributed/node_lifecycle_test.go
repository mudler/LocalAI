package distributed_test

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Node Backend Lifecycle (NATS-driven)", Label("Distributed"), func() {
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

	Context("NATS backend.install events", func() {
		It("should send backend.install request-reply to a specific node", func() {
			node := &nodes.BackendNode{
				Name: "gpu-node-1", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Simulate worker subscribing to backend.install and replying success
			infra.NC.SubscribeReply(messaging.SubjectNodeBackendInstall(node.ID), func(data []byte, reply func([]byte)) {
				var req messaging.BackendInstallRequest
				json.Unmarshal(data, &req)
				Expect(req.Backend).To(Equal("llama-cpp"))

				resp := messaging.BackendInstallReply{Success: true}
				respData, _ := json.Marshal(resp)
				reply(respData)
			})

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			installReply, err := adapter.InstallBackend(node.ID, "llama-cpp", "", "", "", "", "", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(installReply.Success).To(BeTrue())
		})

		It("should propagate error from worker on failed install", func() {
			node := &nodes.BackendNode{
				Name: "fail-node", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Simulate worker replying with error
			infra.NC.SubscribeReply(messaging.SubjectNodeBackendInstall(node.ID), func(data []byte, reply func([]byte)) {
				resp := messaging.BackendInstallReply{Success: false, Error: "backend not found"}
				respData, _ := json.Marshal(resp)
				reply(respData)
			})

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			installReply, err := adapter.InstallBackend(node.ID, "nonexistent", "", "", "", "", "", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(installReply.Success).To(BeFalse())
			Expect(installReply.Error).To(ContainSubstring("backend not found"))
		})
	})

	Context("NATS backend.stop events (model unload)", func() {
		It("should send backend.stop to nodes hosting the model", func() {
			node := &nodes.BackendNode{
				Name: "gpu-node-2", Address: "h2:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "whisper-large", 0, "loaded", "", 0)).To(Succeed())

			var stopReceived atomic.Int32
			sub, err := infra.NC.Subscribe(messaging.SubjectNodeBackendStop(node.ID), func(data []byte) {
				stopReceived.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			// Frontend calls UnloadRemoteModel (triggered by UI "Stop" or WatchDog)
			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
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
			sub1, _ := infra.NC.Subscribe(messaging.SubjectNodeBackendStop(node1.ID), func(data []byte) {
				count.Add(1)
			})
			sub2, _ := infra.NC.Subscribe(messaging.SubjectNodeBackendStop(node2.ID), func(data []byte) {
				count.Add(1)
			})
			defer sub1.Unsubscribe()
			defer sub2.Unsubscribe()

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			adapter.UnloadRemoteModel("shared-model")

			Eventually(func() int32 { return count.Load() }, "5s").Should(Equal(int32(2)))
		})

		It("should be no-op for models not on any node", func() {
			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			Expect(adapter.UnloadRemoteModel("nonexistent-model")).To(Succeed())
		})
	})

	Context("NATS node stop events (full shutdown)", func() {
		It("should publish stop event to a node", func() {
			node := &nodes.BackendNode{
				Name: "stop-me", Address: "h3:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			var stopped atomic.Int32
			sub, err := infra.NC.Subscribe(messaging.SubjectNodeStop(node.ID), func(data []byte) {
				stopped.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			Expect(adapter.StopNode(node.ID)).To(Succeed())

			Eventually(func() int32 { return stopped.Load() }, "5s").Should(Equal(int32(1)))
		})
	})

	Context("NATS subject naming", func() {
		It("should generate correct backend lifecycle subjects", func() {
			Expect(messaging.SubjectNodeBackendInstall("node-abc")).To(Equal("nodes.node-abc.backend.install"))
			Expect(messaging.SubjectNodeBackendStop("node-abc")).To(Equal("nodes.node-abc.backend.stop"))
			Expect(messaging.SubjectNodeStop("node-abc")).To(Equal("nodes.node-abc.stop"))
		})
	})

	// Design note: LoadModel is a direct gRPC call to node.Address, NOT a NATS event.
	// NATS is used for backend.install (install + start process) and backend.stop.
	// The SmartRouter calls grpc.NewClient(node.Address).LoadModel() directly.
	//
	// Flow:
	// 1. NATS backend.install → worker installs backend + starts gRPC process
	// 2. SmartRouter.Route() → gRPC LoadModel(node.Address) directly
	// 3. [inference via gRPC]
	// 4. NATS backend.stop → worker stops gRPC process
})
