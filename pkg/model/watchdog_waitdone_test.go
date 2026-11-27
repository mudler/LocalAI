package model

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A watchdog with only the LRU limit has no Run loop: eviction runs on load.
// A restart of the watchdog shuts it down and waits for the loop, and must
// not wait for a loop that never started.
var _ = Describe("Watchdog shutdown without a run loop", func() {
	It("returns from WaitDone when Run never started", func() {
		wd := NewWatchDog(WithLRULimit(2))
		wd.Shutdown()

		done := make(chan struct{})
		go func() {
			wd.WaitDone()
			close(done)
		}()

		Eventually(done, 2*time.Second).Should(BeClosed())
	})

	It("still waits for a Run loop that started", func() {
		wd := NewWatchDog(WithIdleCheck(true), WithWatchdogInterval(time.Hour))
		go wd.Run()
		Eventually(wd.started.Load).Should(BeTrue())
		wd.Shutdown()

		done := make(chan struct{})
		go func() {
			wd.WaitDone()
			close(done)
		}()

		Eventually(done, 2*time.Second).Should(BeClosed())
	})
})
