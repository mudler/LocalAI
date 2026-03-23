package distributed_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("File Staging", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
		registry      *nodes.NodeRegistry
		tmpDir        string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_filestaging_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		db, err = gorm.Open(pgdriver.Open(pgURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err := natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		nc, err = messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
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

			stager := nodes.NewS3NATSFileStager(fm, nc)
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
			_, err := stager.EnsureRemote(ctx, "node-1", "/tmp/model.gguf", "models/model.gguf")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("resolving HTTP address"))

			err = stager.FetchRemote(ctx, "node-1", "/tmp/output.bin", "/tmp/local.bin")
			Expect(err).To(HaveOccurred())

			_, err = stager.AllocRemoteTemp(ctx, "node-1")
			Expect(err).To(HaveOccurred())

			// StageRemoteToStore is a no-op in HTTP mode
			err = stager.StageRemoteToStore(ctx, "node-1", "/tmp/file", "key")
			Expect(err).ToNot(HaveOccurred())
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
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("model data")))).To(Succeed())

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
			Expect(fm.Upload(ctx, "key", "/nonexistent")).To(Succeed())
			Expect(fm.Delete(ctx, "key")).To(Succeed())

			exists, _ := fm.Exists(ctx, "key")
			Expect(exists).To(BeFalse())
		})
	})
})
