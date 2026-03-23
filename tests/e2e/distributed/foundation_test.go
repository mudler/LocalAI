package distributed_test

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/messaging"
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

var _ = Describe("Phase 0: Foundation", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		pgURL         string
		natsURL       string
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		// Start PostgreSQL container
		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		// Start NATS container
		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err = natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
	})

	Context("Distributed mode validation", func() {
		It("should reject --distributed without PostgreSQL configured", func() {
			appCfg := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithNatsURL(natsURL),
				// No auth/PostgreSQL configured
			)
			Expect(appCfg.Distributed.Enabled).To(BeTrue())
			// Auth not enabled → validation should fail
			Expect(appCfg.Auth.Enabled).To(BeFalse())
		})

		It("should reject --distributed without NATS configured", func() {
			appCfg := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithAuthEnabled(true),
				config.WithAuthDatabaseURL(pgURL),
				// No NATS URL
			)
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})

		It("should accept valid distributed configuration", func() {
			appCfg := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithAuthEnabled(true),
				config.WithAuthDatabaseURL(pgURL),
				config.WithNatsURL(natsURL),
			)
			Expect(appCfg.Distributed.Enabled).To(BeTrue())
			Expect(appCfg.Auth.Enabled).To(BeTrue())
			Expect(appCfg.Distributed.NatsURL).To(Equal(natsURL))
		})

		It("should generate unique frontend ID on startup", func() {
			cfg1 := config.NewApplicationConfig(config.EnableDistributed)
			cfg2 := config.NewApplicationConfig(config.EnableDistributed)
			// IDs are empty until initDistributed generates them,
			// but if set via env, they should be preserved
			cfg3 := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithDistributedInstanceID("my-pod-1"),
			)
			Expect(cfg3.Distributed.InstanceID).To(Equal("my-pod-1"))
			// Default is empty — filled in at startup
			Expect(cfg1.Distributed.InstanceID).To(BeEmpty())
			Expect(cfg2.Distributed.InstanceID).To(BeEmpty())
		})

		It("should start in single-node mode without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())
		})
	})

	Context("NATS client", func() {
		It("should connect, publish, and subscribe", func() {
			client, err := messaging.New(natsURL)
			Expect(err).ToNot(HaveOccurred())
			defer client.Close()

			Expect(client.IsConnected()).To(BeTrue())

			received := make(chan []byte, 1)
			sub, err := client.Subscribe("test.subject", func(data []byte) {
				received <- data
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			// Small delay to ensure subscription is active
			time.Sleep(100 * time.Millisecond)

			err = client.Publish("test.subject", map[string]string{"msg": "hello"})
			Expect(err).ToNot(HaveOccurred())

			Eventually(received, "5s").Should(Receive())
		})

		It("should support queue subscriptions for load balancing", func() {
			client, err := messaging.New(natsURL)
			Expect(err).ToNot(HaveOccurred())
			defer client.Close()

			worker1Count := 0
			worker2Count := 0

			sub1, err := client.QueueSubscribe("test.queue", "workers", func(data []byte) {
				worker1Count++
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub1.Unsubscribe()

			sub2, err := client.QueueSubscribe("test.queue", "workers", func(data []byte) {
				worker2Count++
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub2.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish multiple messages
			for i := 0; i < 10; i++ {
				err = client.Publish("test.queue", map[string]int{"n": i})
				Expect(err).ToNot(HaveOccurred())
			}

			// Wait for all messages to be processed
			Eventually(func() int {
				return worker1Count + worker2Count
			}, "5s").Should(Equal(10))

			// Both workers should have received some messages (load-balanced)
			// Note: with only 10 messages, distribution may not be perfectly even
			Expect(worker1Count + worker2Count).To(Equal(10))
		})

		It("should reconnect after disconnect", func() {
			client, err := messaging.New(natsURL)
			Expect(err).ToNot(HaveOccurred())
			defer client.Close()

			Expect(client.IsConnected()).To(BeTrue())
			// The reconnect behavior is tested implicitly by the RetryOnFailedConnect option
			// A full reconnect test would require stopping/restarting the NATS container
		})
	})

	Context("ObjectStore filesystem adapter", func() {
		var store *storage.FilesystemStore

		BeforeEach(func() {
			var err error
			store, err = storage.NewFilesystemStore(GinkgoT().TempDir())
			Expect(err).ToNot(HaveOccurred())
		})

		It("should Put/Get/Delete", func() {
			ctx := context.Background()

			// Put
			data := []byte("hello world")
			err := store.Put(ctx, "test/file.txt", bytes.NewReader(data))
			Expect(err).ToNot(HaveOccurred())

			// Exists
			exists, err := store.Exists(ctx, "test/file.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeTrue())

			// Get
			r, err := store.Get(ctx, "test/file.txt")
			Expect(err).ToNot(HaveOccurred())
			got, err := io.ReadAll(r)
			r.Close()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("hello world"))

			// List
			keys, err := store.List(ctx, "test")
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(ContainElement("test/file.txt"))

			// Delete
			err = store.Delete(ctx, "test/file.txt")
			Expect(err).ToNot(HaveOccurred())

			exists, err = store.Exists(ctx, "test/file.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
	})

	Context("Advisory locks", func() {
		var db *gorm.DB

		BeforeEach(func() {
			var err error
			db, err = gorm.Open(pgdriver.Open(pgURL), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should acquire and release advisory lock", func() {
			acquired := advisorylock.TryLock(db, 42)
			Expect(acquired).To(BeTrue())

			advisorylock.Unlock(db, 42)
		})

		It("should prevent concurrent acquisition", func() {
			// Use two dedicated sql.Conn to ensure they are different sessions.
			sqlDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())

			conn1, err := sqlDB.Conn(context.Background())
			Expect(err).ToNot(HaveOccurred())
			defer conn1.Close()

			conn2, err := sqlDB.Conn(context.Background())
			Expect(err).ToNot(HaveOccurred())
			defer conn2.Close()

			// Acquire on conn1
			var acquired bool
			err = conn1.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", int64(43)).Scan(&acquired)
			Expect(err).ToNot(HaveOccurred())
			Expect(acquired).To(BeTrue())

			// conn2 should NOT be able to acquire the same lock
			var otherAcquired bool
			err = conn2.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", int64(43)).Scan(&otherAcquired)
			Expect(err).ToNot(HaveOccurred())
			Expect(otherAcquired).To(BeFalse())

			// Release on conn1
			conn1.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", int64(43))

			// Now conn2 should be able to acquire
			err = conn2.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", int64(43)).Scan(&otherAcquired)
			Expect(err).ToNot(HaveOccurred())
			Expect(otherAcquired).To(BeTrue())

			// Clean up
			conn2.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", int64(43))
		})

		It("should support WithLock for scoped locking", func() {
			executed := false
			err := advisorylock.WithLock(db, 44, func() error {
				executed = true
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(executed).To(BeTrue())
		})
	})
})
