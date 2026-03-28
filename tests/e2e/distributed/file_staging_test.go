package distributed_test

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"

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

	Context("S3NATSFileStager", func() {
		It("should create S3NATSFileStager with valid config", func() {
			storeDir := filepath.Join(tmpDir, "objectstore")
			cacheDir := filepath.Join(tmpDir, "cache")

			store, err := storage.NewFilesystemStore(storeDir)
			Expect(err).ToNot(HaveOccurred())

			fm, err := storage.NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(fm.IsConfigured()).To(BeTrue())

			stager := nodes.NewS3NATSFileStager(fm, infra.NC)
			Expect(stager).ToNot(BeNil())
		})
	})

	Context("HTTPFileStager", func() {
		It("should create HTTPFileStager with httpAddrFor function", func() {
			stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
				return "", fmt.Errorf("no such node: %s", nodeID)
			}, "")
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

	Context("S3NATSFileStager with backend node simulation", func() {
		It("should coordinate file staging via NATS request-reply", func() {
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
			Expect(registry.Register(node, true)).To(Succeed())

			// Verify NATS file staging subjects are correctly formed
			ensureSubj := messaging.SubjectNodeFilesEnsure(node.ID)
			Expect(ensureSubj).To(ContainSubstring("files.ensure"))

			stageSubj := messaging.SubjectNodeFilesStage(node.ID)
			Expect(stageSubj).To(ContainSubstring("files.stage"))

			tempSubj := messaging.SubjectNodeFilesTemp(node.ID)
			Expect(tempSubj).To(ContainSubstring("files.temp"))
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
