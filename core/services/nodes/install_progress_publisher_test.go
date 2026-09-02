package nodes

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// collectingSink records every event the debouncer emits. Emits arrive from the
// trailing timer's own goroutine as well as from OnDownload, so it locks.
type collectingSink struct {
	mu     sync.Mutex
	events []messaging.BackendInstallProgressEvent
}

func (c *collectingSink) emit(ev messaging.BackendInstallProgressEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *collectingSink) snapshot() []messaging.BackendInstallProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]messaging.BackendInstallProgressEvent(nil), c.events...)
}

var _ = Describe("DebouncedInstallProgressPublisher", func() {
	It("emits the first event immediately and debounces subsequent ones within the window", func() {
		sink := &collectingSink{}
		pub := NewDebouncedInstallProgressSink(sink.emit, "n1", "op1", "vllm", 50*time.Millisecond)

		// Three rapid-fire ticks within the debounce window.
		pub.OnDownload("vllm.tar.zst", "100 MB", "1 GB", 10.0)
		pub.OnDownload("vllm.tar.zst", "200 MB", "1 GB", 20.0)
		pub.OnDownload("vllm.tar.zst", "300 MB", "1 GB", 30.0)
		pub.Flush()

		// First event emits immediately; the others coalesce; Flush guarantees a final.
		// So we expect at least 2 emits and at most 4 (lead + final + any window-bounded).
		Eventually(func() int { return len(sink.snapshot()) }, "1s").Should(BeNumerically(">=", 2))
		Expect(len(sink.snapshot())).To(BeNumerically("<=", 4),
			"three ticks within the debounce window should produce at most ~4 emits")
	})

	It("emits the final event after Flush with the latest percentage", func() {
		sink := &collectingSink{}
		pub := NewDebouncedInstallProgressSink(sink.emit, "n1", "op1", "vllm", 50*time.Millisecond)

		pub.OnDownload("vllm.tar.zst", "1 GB", "1 GB", 100.0)
		pub.Flush()

		Eventually(func() float64 {
			events := sink.snapshot()
			if len(events) == 0 {
				return -1
			}
			return events[len(events)-1].Percentage
		}, "1s").Should(Equal(100.0))
	})

	It("stamps every event with the identity the frontend correlates on", func() {
		// The op id and node id are what a frontend matches a progress line to
		// an operation with; the subject used to carry them and now nothing
		// else does, so the event body has to.
		sink := &collectingSink{}
		pub := NewDebouncedInstallProgressSink(sink.emit, "node-7", "op-42", "vllm", time.Millisecond)
		pub.OnDownload("vllm.tar.zst", "1 GB", "1 GB", 100.0)
		pub.Flush()

		Eventually(func() int { return len(sink.snapshot()) }, "1s").Should(BeNumerically(">=", 1))
		ev := sink.snapshot()[0]
		Expect(ev.NodeID).To(Equal("node-7"))
		Expect(ev.OpID).To(Equal("op-42"))
		Expect(ev.Backend).To(Equal("vllm"))
		Expect(ev.FileName).To(Equal("vllm.tar.zst"))
		Expect(ev.Phase).To(Equal(messaging.PhaseDownloading))
	})
})
