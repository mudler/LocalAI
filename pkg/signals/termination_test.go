package signals

import (
	"os"
	"os/signal"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// isolateTerminationChannels hides the channel of the package's own handler,
// which would end the test process, for the length of one test.
func isolateTerminationChannels() {
	terminationMu.Lock()
	saved := terminationChannels
	terminationChannels = nil
	terminationMu.Unlock()
	DeferCleanup(func() {
		terminationMu.Lock()
		terminationChannels = saved
		terminationMu.Unlock()
	})
}

var _ = Describe("NotifyTermination", func() {
	It("delivers a stop request that is not an OS signal as SIGTERM", func() {
		isolateTerminationChannels()
		c := make(chan os.Signal, 1)
		NotifyTermination(c)
		DeferCleanup(func() { signalStop(c) })

		deliverTermination()
		Eventually(c).Should(Receive(Equal(syscall.SIGTERM)))
	})

	It("does not block on a full channel", func() {
		isolateTerminationChannels()
		c := make(chan os.Signal) // no room
		NotifyTermination(c)
		DeferCleanup(func() { signalStop(c) })

		done := make(chan struct{})
		go func() { deliverTermination(); close(done) }()
		Eventually(done).Should(BeClosed())
	})
})

func signalStop(c chan os.Signal) { signal.Stop(c) }
