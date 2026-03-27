package distributed_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Model and Backend Managers", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_managers_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("LocalModelManager", func() {
		var (
			tempDir     string
			ss          *system.SystemState
			ml          *model.ModelLoader
			localMgr    *galleryop.LocalModelManager
		)

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "manager-model-test-*")
			Expect(err).ToNot(HaveOccurred())

			ss, err = system.GetSystemState(system.WithModelPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			ml = model.NewModelLoader(ss)

			appCfg := config.NewApplicationConfig()
			appCfg.SystemState = ss
			localMgr = galleryop.NewLocalModelManager(appCfg, ml)
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("should delete a model from the local filesystem", func() {
			// Create a fake model config file
			modelName := "test-model"
			configFile := filepath.Join(tempDir, modelName+".yaml")
			Expect(os.WriteFile(configFile, []byte("name: test-model\n"), 0644)).To(Succeed())

			err := localMgr.DeleteModel(modelName)
			Expect(err).ToNot(HaveOccurred())

			// Assert file is gone
			_, err = os.Stat(configFile)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Context("LocalBackendManager", func() {
		var (
			tempDir  string
			ss       *system.SystemState
			ml       *model.ModelLoader
			localMgr *galleryop.LocalBackendManager
		)

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "manager-backend-test-*")
			Expect(err).ToNot(HaveOccurred())

			ss, err = system.GetSystemState(system.WithBackendPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			ml = model.NewModelLoader(ss)

			appCfg := config.NewApplicationConfig()
			appCfg.SystemState = ss
			localMgr = galleryop.NewLocalBackendManager(appCfg, ml)
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("should delete a backend from the local filesystem", func() {
			// Create a fake backend directory with run.sh
			backendName := "test-backend"
			backendDir := filepath.Join(tempDir, backendName)
			Expect(os.MkdirAll(backendDir, 0750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(backendDir, "run.sh"), []byte("#!/bin/bash\necho test"), 0755)).To(Succeed())

			err := localMgr.DeleteBackend(backendName)
			Expect(err).ToNot(HaveOccurred())

			// Assert directory is gone
			_, err = os.Stat(backendDir)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Context("DistributedModelManager", func() {
		It("should delete model locally AND send model.delete to worker nodes", func() {
			// Register two nodes with the model
			node1 := &nodes.BackendNode{Name: "dm-n1", Address: "h1:50051"}
			node2 := &nodes.BackendNode{Name: "dm-n2", Address: "h2:50051"}
			Expect(registry.Register(node1, true)).To(Succeed())
			Expect(registry.Register(node2, true)).To(Succeed())
			Expect(registry.SetNodeModel(node1.ID, "big-model", "loaded")).To(Succeed())
			Expect(registry.SetNodeModel(node2.ID, "big-model", "loaded")).To(Succeed())

			// Subscribe to model.delete on both node subjects, track receipt
			var deleteCount atomic.Int32
			sub1, err := infra.NC.SubscribeReply(messaging.SubjectNodeModelDelete(node1.ID), func(data []byte, reply func([]byte)) {
				var req messaging.ModelDeleteRequest
				json.Unmarshal(data, &req)
				Expect(req.ModelName).To(Equal("big-model"))
				deleteCount.Add(1)
				resp, _ := json.Marshal(messaging.ModelDeleteReply{Success: true})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub1.Unsubscribe()

			sub2, err := infra.NC.SubscribeReply(messaging.SubjectNodeModelDelete(node2.ID), func(data []byte, reply func([]byte)) {
				var req messaging.ModelDeleteRequest
				json.Unmarshal(data, &req)
				deleteCount.Add(1)
				resp, _ := json.Marshal(messaging.ModelDeleteReply{Success: true})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub2.Unsubscribe()

			FlushNATS(infra.NC)

			// Create temp dir for local model files
			tempDir, err := os.MkdirTemp("", "dist-model-test-*")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempDir)

			// Create a fake model config file
			modelFile := filepath.Join(tempDir, "big-model.yaml")
			Expect(os.WriteFile(modelFile, []byte("name: big-model\n"), 0644)).To(Succeed())

			ss, err := system.GetSystemState(system.WithModelPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			ml := model.NewModelLoader(ss)
			appCfg := config.NewApplicationConfig()
			appCfg.SystemState = ss

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			distMgr := nodes.NewDistributedModelManager(appCfg, ml, adapter)

			err = distMgr.DeleteModel("big-model")
			Expect(err).ToNot(HaveOccurred())

			// Local file should be deleted
			_, statErr := os.Stat(modelFile)
			Expect(os.IsNotExist(statErr)).To(BeTrue())

			// Both workers should have received model.delete
			Eventually(func() int32 { return deleteCount.Load() }, "5s").Should(Equal(int32(2)))
		})
	})

	Context("DistributedBackendManager", func() {
		It("should delete backend locally AND fan out backend.delete to all healthy nodes", func() {
			// Register 3 nodes: 2 healthy, 1 unhealthy
			node1 := &nodes.BackendNode{Name: "db-n1", Address: "h1:50051"}
			node2 := &nodes.BackendNode{Name: "db-n2", Address: "h2:50051"}
			node3 := &nodes.BackendNode{Name: "db-n3", Address: "h3:50051"}
			Expect(registry.Register(node1, true)).To(Succeed())
			Expect(registry.Register(node2, true)).To(Succeed())
			Expect(registry.Register(node3, true)).To(Succeed())
			Expect(registry.MarkUnhealthy(node3.ID)).To(Succeed())

			// Subscribe to backend.delete on all 3 nodes
			var deleteCount atomic.Int32
			sub1, err := infra.NC.SubscribeReply(messaging.SubjectNodeBackendDelete(node1.ID), func(data []byte, reply func([]byte)) {
				var req messaging.BackendDeleteRequest
				json.Unmarshal(data, &req)
				Expect(req.Backend).To(Equal("my-backend"))
				deleteCount.Add(1)
				resp, _ := json.Marshal(messaging.BackendDeleteReply{Success: true})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub1.Unsubscribe()

			sub2, err := infra.NC.SubscribeReply(messaging.SubjectNodeBackendDelete(node2.ID), func(data []byte, reply func([]byte)) {
				var req messaging.BackendDeleteRequest
				json.Unmarshal(data, &req)
				deleteCount.Add(1)
				resp, _ := json.Marshal(messaging.BackendDeleteReply{Success: true})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub2.Unsubscribe()

			var unhealthyReceived atomic.Int32
			sub3, err := infra.NC.SubscribeReply(messaging.SubjectNodeBackendDelete(node3.ID), func(data []byte, reply func([]byte)) {
				unhealthyReceived.Add(1)
				resp, _ := json.Marshal(messaging.BackendDeleteReply{Success: true})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub3.Unsubscribe()

			FlushNATS(infra.NC)

			// Create temp dir for local backend files
			tempDir, err := os.MkdirTemp("", "dist-backend-test-*")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempDir)

			// Create a fake backend directory
			backendDir := filepath.Join(tempDir, "my-backend")
			Expect(os.MkdirAll(backendDir, 0750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(backendDir, "run.sh"), []byte("#!/bin/bash\necho test"), 0755)).To(Succeed())

			ss, err := system.GetSystemState(system.WithBackendPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			ml := model.NewModelLoader(ss)
			appCfg := config.NewApplicationConfig()
			appCfg.SystemState = ss

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			distMgr := nodes.NewDistributedBackendManager(appCfg, ml, adapter, registry)

			err = distMgr.DeleteBackend("my-backend")
			Expect(err).ToNot(HaveOccurred())

			// Local backend dir should be deleted
			_, statErr := os.Stat(backendDir)
			Expect(os.IsNotExist(statErr)).To(BeTrue())

			// 2 healthy nodes should have received backend.delete
			Eventually(func() int32 { return deleteCount.Load() }, "5s").Should(Equal(int32(2)))

			// Unhealthy node should NOT have received backend.delete
			Consistently(func() int32 { return unhealthyReceived.Load() }, "1s").Should(Equal(int32(0)))
		})

		It("should succeed when backend exists only on remote workers (not locally)", func() {
			// Register a healthy node
			node1 := &nodes.BackendNode{Name: "db-remote-only", Address: "h1:50051"}
			Expect(registry.Register(node1, true)).To(Succeed())

			var deleteCount atomic.Int32
			sub1, err := infra.NC.SubscribeReply(messaging.SubjectNodeBackendDelete(node1.ID), func(data []byte, reply func([]byte)) {
				var req messaging.BackendDeleteRequest
				json.Unmarshal(data, &req)
				Expect(req.Backend).To(Equal("remote-only-backend"))
				deleteCount.Add(1)
				resp, _ := json.Marshal(messaging.BackendDeleteReply{Success: true})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub1.Unsubscribe()

			FlushNATS(infra.NC)

			// Use a temp dir with NO local backend directory — simulates frontend node
			tempDir, err := os.MkdirTemp("", "dist-backend-remote-only-*")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempDir)

			ss, err := system.GetSystemState(system.WithBackendPath(tempDir))
			Expect(err).ToNot(HaveOccurred())
			ml := model.NewModelLoader(ss)
			appCfg := config.NewApplicationConfig()
			appCfg.SystemState = ss

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
			distMgr := nodes.NewDistributedBackendManager(appCfg, ml, adapter, registry)

			// Should NOT return an error even though the backend doesn't exist locally
			err = distMgr.DeleteBackend("remote-only-backend")
			Expect(err).ToNot(HaveOccurred())

			// The healthy worker should still receive the deletion request
			Eventually(func() int32 { return deleteCount.Load() }, "5s").Should(Equal(int32(1)))
		})
	})
})
