package advisorylock

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

var _ = Describe("AdvisoryLock", func() {
	Context("PostgreSQL advisory locks", func() {
		var db *gorm.DB

		BeforeEach(func() {
			if runtime.GOOS == "darwin" {
				Skip("testcontainers requires Docker, not available on macOS CI")
			}
			db = testutil.SetupTestDB()
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

		It("acquires and releases with TryWithLockCtx", func() {
			const lockKey int64 = 888

			acquired, err := TryWithLockCtx(context.Background(), db, lockKey, func() error {
				// Lock is held here
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(acquired).To(BeTrue(), "expected TryWithLockCtx to acquire the lock")

			// Lock released — should be re-acquirable
			reacquired, err := TryWithLockCtx(context.Background(), db, lockKey, func() error {
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(reacquired).To(BeTrue(), "expected TryWithLockCtx to re-acquire the lock")
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
				KeyCronScheduler:    "KeyCronScheduler",
				KeyStaleNodeCleanup: "KeyStaleNodeCleanup",
				KeyGalleryDedup:     "KeyGalleryDedup",
				KeyAgentScheduler:   "KeyAgentScheduler",
				KeyHealthCheck:      "KeyHealthCheck",
				KeySchemaMigrate:    "KeySchemaMigrate",
			}

			Expect(keys).To(HaveLen(6), "some advisory lock keys have the same value")
		})

		It("KeyFromString is deterministic", func() {
			k1 := KeyFromString("foo")
			k2 := KeyFromString("foo")
			Expect(k1).To(Equal(k2), "KeyFromString should return the same value for the same input")
		})

		It("KeyFromString returns different keys for different inputs", func() {
			kFoo := KeyFromString("foo")
			kBar := KeyFromString("bar")
			Expect(kFoo).ToNot(Equal(kBar), "KeyFromString should return different keys for different inputs")
		})
	})

	Context("WithLockCtx (PostgreSQL)", func() {
		var db *gorm.DB

		BeforeEach(func() {
			if runtime.GOOS == "darwin" {
				Skip("testcontainers requires Docker, not available on macOS CI")
			}
			db = testutil.SetupTestDB()
		})

		It("acquires lock and executes the function", func() {
			const lockKey int64 = 700
			executed := false

			err := WithLockCtx(context.Background(), db, lockKey, func() error {
				executed = true
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(executed).To(BeTrue(), "function should have been executed under lock")
		})

		It("returns error when context is already cancelled", func() {
			const lockKey int64 = 701
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			err := WithLockCtx(ctx, db, lockKey, func() error {
				Fail("function should not run with cancelled context")
				return nil
			})
			Expect(err).To(HaveOccurred())
		})

		It("serializes concurrent WithLockCtx on same key", func() {
			const lockKey int64 = 702

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
	})
})
