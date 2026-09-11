package distributed_test

import (
	"bytes"
	"context"
	"io"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
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
				// No auth/PostgreSQL configured
			)
			Expect(appCfg.Distributed.Enabled).To(BeTrue())
			// Auth not enabled → validation should fail
			Expect(appCfg.Auth.Enabled).To(BeFalse())
		})

		// Two Its stood here: "leaves the inert bus URL empty when nothing sets
		// it" and "should accept valid distributed configuration", which passed
		// config.WithNatsURL and read the value back. Both are retired with the
		// field and the option they used, and neither property is lost.
		//
		// "The value is never dialled" is no longer a promise about a stored
		// value; there is nowhere to store one, which core/config's "broker
		// surface" spec asserts by reflection so it cannot silently stop
		// compiling when the field returns. "An existing command line still
		// starts" moved DOWN a level, to where it is actually at risk: kong is
		// what rejects an unknown flag, so core/cli's "frontend's broker flags"
		// specs parse the real command line, and the cluster suite starts real
		// frontends with a dead LOCALAI_NATS_URL in their environment.
		It("should accept a valid distributed configuration", func() {
			appCfg := config.NewApplicationConfig(
				config.EnableDistributed,
				config.WithAuthEnabled(true),
				config.WithAuthDatabaseURL(infra.PGURL),
			)
			Expect(appCfg.Distributed.Enabled).To(BeTrue())
			Expect(appCfg.Auth.Enabled).To(BeTrue())
			Expect(appCfg.Distributed.Validate()).To(Succeed())
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
	//
	// The third went with the family it carried. A cancel used to be published
	// on NATS because its subscriber was an agent worker that could not read
	// the broadcast carrier; it is a control verb on that worker's own tunnel
	// now, and the deployment dials no message bus at all. The path is driven
	// end to end, over a real tunnel, in agent_distributed_test.go.

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
