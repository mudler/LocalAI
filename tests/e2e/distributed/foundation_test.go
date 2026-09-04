package distributed_test

import (
	"bytes"
	"context"
	"io"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Phase 0: Foundation", Label("Distributed"), func() {
	var (
		infra *TestInfra
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_test")
	})

	Context("Distributed mode validation", func() {
		It("should reject --distributed without PostgreSQL configured", func() {
			appCfg := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithNatsURL(infra.NatsURL),
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
				config.WithAuthDatabaseURL(infra.PGURL),
				// No NATS URL
			)
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})

		It("should accept valid distributed configuration", func() {
			appCfg := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithAuthEnabled(true),
				config.WithAuthDatabaseURL(infra.PGURL),
				config.WithNatsURL(infra.NatsURL),
			)
			Expect(appCfg.Distributed.Enabled).To(BeTrue())
			Expect(appCfg.Auth.Enabled).To(BeTrue())
			Expect(appCfg.Distributed.NatsURL).To(Equal(infra.NatsURL))
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

	// The one carrier a deployment still dials besides PostgreSQL, and the one
	// family left on it.
	//
	// agent.<name>.cancel did not move to the broadcast carrier: its only
	// subscriber is the agent WORKER, which has no database and cannot join
	// that carrier at all. So this round trip is no longer "the messaging layer
	// works" - it is the cancel path for every agent a worker runs, and if it
	// stops working the symptom is a cancel that is published, succeeds, and
	// reaches nobody.
	//
	// Two Its that used to sit here went with the halves of the client they
	// exercised. "should support queue subscriptions for load balancing" pinned
	// that work reaches exactly one of N consumers; a queue group no longer
	// selects anything and that property is now core/services/jobs
	// claim_test.go's competing-claimants specs, which race eight claimants for
	// eight rows and then for one. "should reconnect after disconnect" pinned
	// that the carrier survives a drop, and said in its own body that it
	// asserted nothing of the kind; the property is now
	// core/services/pgbus/listener_test.go, which actually kills the session
	// with pg_terminate_backend and waits for delivery to resume.
	Context("the cancel carrier", func() {
		It("connects, publishes and subscribes, which is the agent cancel path", func() {
			client, err := messaging.New(infra.NatsURL)
			Expect(err).ToNot(HaveOccurred())
			defer client.Close()

			Expect(client.IsConnected()).To(BeTrue())

			// The real subject and the real filter, not a placeholder pair.
			// A worker subscribes to the wildcard and a frontend publishes to
			// one agent's subject, so a round trip on "test.subject" would
			// stay green through a filter that no longer matches what the
			// builder mints - which is the failure this family actually has.
			received := make(chan []byte, 1)
			sub, err := client.Subscribe(messaging.SubjectAgentCancelWildcard, func(data []byte) {
				received <- data
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			// Small delay to ensure subscription is active
			FlushNATS(client)

			err = client.Publish(messaging.SubjectAgentCancel("a1"), agents.AgentCancelEvent{
				AgentName: "a1", UserID: "u1", MessageID: "msg-1",
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(received, "5s").Should(Receive())
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
			db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should acquire and release advisory lock", func() {
			executed := false
			acquired, err := advisorylock.TryWithLockCtx(context.Background(), db, 42, func() error {
				executed = true
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(acquired).To(BeTrue())
			Expect(executed).To(BeTrue())
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

		It("should support WithLockCtx for scoped locking", func() {
			executed := false
			err := advisorylock.WithLockCtx(context.Background(), db, 44, func() error {
				executed = true
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(executed).To(BeTrue())
		})
	})
})
