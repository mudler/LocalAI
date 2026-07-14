package nodes

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("DebouncedInstallProgressPublisher", func() {
	It("publishes the first event immediately and debounces subsequent ones within the window", func() {
		mc := newScriptedMessagingClient()
		pub := NewDebouncedInstallProgressPublisher(mc, "n1", "op1", "vllm", 50*time.Millisecond)

		// Three rapid-fire ticks within the debounce window.
		pub.OnDownload("vllm.tar.zst", "100 MB", "1 GB", 10.0)
		pub.OnDownload("vllm.tar.zst", "200 MB", "1 GB", 20.0)
		pub.OnDownload("vllm.tar.zst", "300 MB", "1 GB", 30.0)
		pub.Flush()

		// First event publishes immediately; the others coalesce; Flush guarantees a final.
		// So we expect at least 2 publishes and at most 4 (lead + final + any window-bounded).
		Eventually(func() int {
			return len(mc.publishCalls(messaging.SubjectNodeBackendInstallProgress("n1", "op1")))
		}, "1s").Should(BeNumerically(">=", 2))
		calls := mc.publishCalls(messaging.SubjectNodeBackendInstallProgress("n1", "op1"))
		Expect(len(calls)).To(BeNumerically("<=", 4),
			"three ticks within the debounce window should produce at most ~4 publishes")
	})

	It("publishes the final event after Flush with the latest percentage", func() {
		mc := newScriptedMessagingClient()
		pub := NewDebouncedInstallProgressPublisher(mc, "n1", "op1", "vllm", 50*time.Millisecond)

		pub.OnDownload("vllm.tar.zst", "1 GB", "1 GB", 100.0)
		pub.Flush()

		Eventually(func() float64 {
			calls := mc.publishCalls(messaging.SubjectNodeBackendInstallProgress("n1", "op1"))
			if len(calls) == 0 {
				return -1
			}
			return calls[len(calls)-1].Percentage
		}, "1s").Should(Equal(100.0))
	})
})
