package distributed_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("File Staging", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
		tmpDir   string
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_filestaging_test")
		tmpDir = GinkgoT().TempDir()

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("S3FileStager", func() {
		It("should create S3FileStager with valid config", func() {
			storeDir := filepath.Join(tmpDir, "objectstore")
			cacheDir := filepath.Join(tmpDir, "cache")

			store, err := storage.NewFilesystemStore(storeDir)
			Expect(err).ToNot(HaveOccurred())

			fm, err := storage.NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(fm.IsConfigured()).To(BeTrue())

			stager := nodes.NewS3FileStager(fm, nodes.NewControlClient(directWorkerDialerFor, ""))
			Expect(stager).ToNot(BeNil())
		})
	})

	Context("HTTPFileStager", func() {
		It("should create HTTPFileStager with httpAddrFor function", func() {
			stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
				return "", fmt.Errorf("no such node: %s", nodeID)
			}, "", directWorkerDialerFor)
			Expect(stager).ToNot(BeNil())

			// Should fail gracefully when node resolution fails
			_, err := stager.EnsureRemote(infra.Ctx, "node-1", "/tmp/model.gguf", "models/model.gguf")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("resolving HTTP address"))

			err = stager.FetchRemote(infra.Ctx, "node-1", "/tmp/output.bin", "/tmp/local.bin")
			Expect(err).To(HaveOccurred())

			_, err = stager.AllocRemoteTemp(infra.Ctx, "node-1")
			Expect(err).To(HaveOccurred())

			// StageRemoteToStore is not supported in HTTP mode
			err = stager.StageRemoteToStore(infra.Ctx, "node-1", "/tmp/file", "key")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not supported"))
		})
	})

	Context("S3FileStager with backend node simulation", func() {
		It("should coordinate file staging over the worker's control plane", func() {
			storeDir := filepath.Join(tmpDir, "objectstore")
			cacheDir := filepath.Join(tmpDir, "cache")

			store, err := storage.NewFilesystemStore(storeDir)
			Expect(err).ToNot(HaveOccurred())

			_, err = storage.NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			// Seed a file in the store to simulate it being in S3
			key := storage.ModelKey("test-model.gguf")
			Expect(store.Put(infra.Ctx, key, bytes.NewReader([]byte("model data")))).To(Succeed())

			node := &nodes.BackendNode{
				Name: "staging-node", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// The staging verbs are HTTP routes on the worker's own control
			// plane now, so what a registered node is addressed by is its
			// tunnel host and the path, not a subject.
			Expect(nodes.WorkerHTTPHost(node.ID, "")).To(ContainSubstring(node.ID))
			Expect(workerctl.PathFilesEnsure).To(HavePrefix(workerctl.Prefix))
			Expect(workerctl.PathFilesStage).To(HavePrefix(workerctl.Prefix))
			Expect(workerctl.PathFilesTemp).To(HavePrefix(workerctl.Prefix))
			Expect(workerctl.PathFilesListDir).To(HavePrefix(workerctl.Prefix))
		})
	})

	Context("Without --distributed", func() {
		It("should pass through unchanged without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, a FileManager with nil store is a no-op
			fm, err := storage.NewFileManager(nil, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(fm.IsConfigured()).To(BeFalse())

			// Upload and download are no-ops
			Expect(fm.Upload(infra.Ctx, "key", "/nonexistent")).To(Succeed())
			Expect(fm.Delete(infra.Ctx, "key")).To(Succeed())

			exists, _ := fm.Exists(infra.Ctx, "key")
			Expect(exists).To(BeFalse())
		})
	})
})
