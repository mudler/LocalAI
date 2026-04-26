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
)

var _ = Describe("RunLeaderLoop", func() {
	Context("PostgreSQL leader election", func() {
		BeforeEach(func() {
			if runtime.GOOS == "darwin" {
				Skip("testcontainers requires Docker, not available on macOS CI")
			}
		})

		It("executes function on tick", func() {
			db := testutil.SetupTestDB()
			const lockKey int64 = 5000

			var callCount int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go RunLeaderLoop(ctx, db, lockKey, 50*time.Millisecond, func() {
				atomic.AddInt32(&callCount, 1)
			})

			Eventually(func() int32 {
				return atomic.LoadInt32(&callCount)
			}, 500*time.Millisecond, 10*time.Millisecond).Should(BeNumerically(">=", 1),
				"expected function to be called at least once")
		})

		It("stops when context is cancelled", func() {
			db := testutil.SetupTestDB()
			const lockKey int64 = 5001

			var callCount int32
			ctx, cancel := context.WithCancel(context.Background())

			done := make(chan struct{})
			go func() {
				RunLeaderLoop(ctx, db, lockKey, 50*time.Millisecond, func() {
					atomic.AddInt32(&callCount, 1)
				})
				close(done)
			}()

			// Let it run a bit then cancel
			time.Sleep(150 * time.Millisecond)
			cancel()

			// RunLeaderLoop should return
			Eventually(done, 500*time.Millisecond).Should(BeClosed())

			// Record count after cancellation
			countAfterCancel := atomic.LoadInt32(&callCount)
			time.Sleep(150 * time.Millisecond)
			countLater := atomic.LoadInt32(&callCount)

			Expect(countLater).To(Equal(countAfterCancel),
				"function should stop being called after context cancellation")
		})

		It("only one leader executes at a time (two concurrent loops)", func() {
			db := testutil.SetupTestDB()
			const lockKey int64 = 5002

			var (
				mu         sync.Mutex
				maxRunning int32
				running    int32
			)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			fn := func() {
				cur := atomic.AddInt32(&running, 1)
				mu.Lock()
				if cur > maxRunning {
					maxRunning = cur
				}
				mu.Unlock()

				time.Sleep(30 * time.Millisecond)

				atomic.AddInt32(&running, -1)
			}

			// Start two competing leader loops with the same lock key
			go RunLeaderLoop(ctx, db, lockKey, 50*time.Millisecond, fn)
			go RunLeaderLoop(ctx, db, lockKey, 50*time.Millisecond, fn)

			// Let them run for a while
			time.Sleep(400 * time.Millisecond)
			cancel()

			mu.Lock()
			observed := maxRunning
			mu.Unlock()

			Expect(observed).To(BeNumerically("<=", 1),
				"expected at most 1 goroutine running the leader function at a time")
		})
	})
})
