package advisorylock

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// These specs run against an in-memory SQLite DB and therefore do NOT require
// Docker, unlike the PostgreSQL testcontainer specs.
var _ = Describe("AdvisoryLock (SQLite fallback)", Label("sqlite"), func() {
	var db *gorm.DB

	BeforeEach(func() {
		var err error
		db, err = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		Expect(db.Dialector.Name()).To(ContainSubstring("sqlite"))
	})

	It("WithLockCtx executes fn and returns no error on SQLite", func() {
		const lockKey int64 = 12001
		executed := false

		err := WithLockCtx(context.Background(), db, lockKey, func() error {
			executed = true
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(executed).To(BeTrue(), "function should have run under the in-process lock")
	})

	It("WithLockCtx serializes concurrent goroutines on the same key", func() {
		const lockKey int64 = 12002

		var (
			mu          sync.Mutex
			maxRunning  int32
			running     int32
			concurrency int32
		)

		var wg sync.WaitGroup

		for range 2 {
			wg.Go(func() {
				defer GinkgoRecover()
				err := WithLockCtx(context.Background(), db, lockKey, func() error {
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
			})
		}

		wg.Wait()

		Expect(maxRunning).To(BeNumerically("<=", 1), "expected max 1 goroutine inside lock at a time")
		Expect(concurrency).To(BeZero(), "detected concurrent execution inside advisory lock")
	})

	It("WithLockCtx returns an error and does not run fn with an already-cancelled context", func() {
		const lockKey int64 = 12003
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := WithLockCtx(ctx, db, lockKey, func() error {
			Fail("function should not run with a cancelled context")
			return nil
		})
		Expect(err).To(HaveOccurred())
	})

	It("TryWithLockCtx returns (true, nil) when free and (false, nil) when held", func() {
		const lockKey int64 = 12004

		acquired, err := TryWithLockCtx(context.Background(), db, lockKey, func() error {
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(acquired).To(BeTrue(), "expected TryWithLockCtx to acquire the free lock")

		// Hold the lock in one goroutine while a concurrent TryWithLockCtx
		// attempts to acquire the same key.
		held := make(chan struct{})
		release := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			defer GinkgoRecover()
			ok, err := TryWithLockCtx(context.Background(), db, lockKey, func() error {
				close(held)
				<-release
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
		})

		<-held
		ok, err := TryWithLockCtx(context.Background(), db, lockKey, func() error {
			Fail("function should not run while lock is held")
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse(), "expected TryWithLockCtx to fail to acquire a held lock")

		close(release)
		wg.Wait()
	})
})
