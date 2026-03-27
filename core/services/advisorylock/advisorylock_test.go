package advisorylock

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB() *gorm.DB {
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { pgC.Terminate(context.Background()) })

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	Expect(err).ToNot(HaveOccurred())

	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())
	return db
}

var _ = Describe("AdvisoryLock", func() {
	Context("PostgreSQL advisory locks", func() {
		var db *gorm.DB

		BeforeEach(func() {
			db = setupTestDB()
		})

		It("prevents concurrent execution with WithLock", func() {
			const lockKey int64 = 999

			var (
				mu          sync.Mutex
				maxRunning  int32
				running     int32
				concurrency int32
			)

			var wg sync.WaitGroup
			wg.Add(2)

			for i := 0; i < 2; i++ {
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					err := WithLock(db, lockKey, func() error {
						cur := atomic.AddInt32(&running, 1)
						mu.Lock()
						if cur > maxRunning {
							maxRunning = cur
						}
						if cur > 1 {
							atomic.AddInt32(&concurrency, 1)
						}
						mu.Unlock()

						time.Sleep(50 * time.Millisecond)

						atomic.AddInt32(&running, -1)
						return nil
					})
					Expect(err).ToNot(HaveOccurred())
				}()
			}

			wg.Wait()

			Expect(maxRunning).To(BeNumerically("<=", 1), "expected max 1 goroutine inside lock at a time")
			Expect(concurrency).To(BeZero(), "detected concurrent execution inside advisory lock")
		})

		It("acquires and unlocks with TryLock", func() {
			const lockKey int64 = 888

			acquired := TryLock(db, lockKey)
			Expect(acquired).To(BeTrue(), "expected TryLock to acquire the lock")

			Unlock(db, lockKey)

			reacquired := TryLock(db, lockKey)
			Expect(reacquired).To(BeTrue(), "expected TryLock to re-acquire the lock after Unlock")

			Unlock(db, lockKey)
		})
	})

	Context("pure logic", func() {
		It("KeyFromUUID is deterministic", func() {
			a := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
			b := [16]byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

			keyA1 := KeyFromUUID(a)
			keyA2 := KeyFromUUID(a)
			keyB := KeyFromUUID(b)

			Expect(keyA1).To(Equal(keyA2), "KeyFromUUID not deterministic")
			Expect(keyA1).ToNot(Equal(keyB), "KeyFromUUID returned same key for different inputs")
		})

		It("all advisory lock keys are unique", func() {
			keys := map[int64]string{
				messaging.AdvisoryLockCronScheduler:    "messaging.AdvisoryLockCronScheduler",
				messaging.AdvisoryLockStaleNodeCleanup: "messaging.AdvisoryLockStaleNodeCleanup",
				messaging.AdvisoryLockGalleryDedup:     "messaging.AdvisoryLockGalleryDedup",
				messaging.AdvisoryLockAgentScheduler:   "messaging.AdvisoryLockAgentScheduler",
				messaging.AdvisoryLockHealthCheck:      "messaging.AdvisoryLockHealthCheck",
				messaging.AdvisoryLockSchemaMigrate:    "messaging.AdvisoryLockSchemaMigrate",
			}

			Expect(keys).To(HaveLen(6), "some advisory lock keys have the same value")
		})
	})
})
