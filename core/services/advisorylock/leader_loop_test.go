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

			Eventually(func() int32 {
				return atomic.LoadInt32(&callCount)
			}, 500*time.Millisecond, 10*time.Millisecond).Should(BeNumerically(">=", 1))
			cancel()

			Eventually(done, 500*time.Millisecond).Should(BeClosed())
		})

		It("only one leader executes at a time (two concurrent loops)", func() {
			db := testutil.SetupTestDB()
			const lockKey int64 = 5002

			var running int32
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{}, 2)
			DeferCleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
			})

			fn := func() {
				atomic.AddInt32(&running, 1)
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
				atomic.AddInt32(&running, -1)
			}

			for range 2 {
				go func() {
					RunLeaderLoop(ctx, db, lockKey, 1*time.Millisecond, fn)
					done <- struct{}{}
				}()
			}

			Eventually(entered, 500*time.Millisecond).Should(Receive())
			Consistently(func() int32 {
				return atomic.LoadInt32(&running)
			}, 50*time.Millisecond, 5*time.Millisecond).Should(Equal(int32(1)),
				"expected only the lock holder to run while both loops tick")

			cancel()
			releaseOnce.Do(func() { close(release) })
			Eventually(done, 500*time.Millisecond).Should(Receive())
			Eventually(done, 500*time.Millisecond).Should(Receive())
		})
	})
})
