package nodes

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// emitRecorder is the emit callback a carrier would hand the sink. The
// trailing debounce fires it from a timer goroutine, hence the lock.
type emitRecorder struct {
	mu     sync.Mutex
	events []workerctl.BackendInstallProgressEvent
}

func (r *emitRecorder) emit(ev workerctl.BackendInstallProgressEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *emitRecorder) emitted() []workerctl.BackendInstallProgressEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]workerctl.BackendInstallProgressEvent(nil), r.events...)
}

var _ = Describe("DebouncedInstallProgressSink", func() {
	It("emits the first event immediately and debounces subsequent ones within the window", func() {
		rec := &emitRecorder{}
		sink := NewDebouncedInstallProgressSink(rec.emit, "n1", "op1", "vllm", 50*time.Millisecond)

		// Three rapid-fire ticks within the debounce window.
		sink.OnDownload("vllm.tar.zst", "100 MB", "1 GB", 10.0)
		sink.OnDownload("vllm.tar.zst", "200 MB", "1 GB", 20.0)
		sink.OnDownload("vllm.tar.zst", "300 MB", "1 GB", 30.0)
		sink.Flush()

		// First event emits immediately; the others coalesce; Flush guarantees a final.
		// So we expect at least 2 emits and at most 4 (lead + final + any window-bounded).
		Eventually(func() int { return len(rec.emitted()) }, "1s").Should(BeNumerically(">=", 2))
		Expect(len(rec.emitted())).To(BeNumerically("<=", 4),
			"three ticks within the debounce window should produce at most ~4 emits")
		for _, ev := range rec.emitted() {
			Expect(ev.OpID).To(Equal("op1"))
			Expect(ev.NodeID).To(Equal("n1"))
			Expect(ev.Backend).To(Equal("vllm"))
			Expect(ev.Phase).To(Equal(workerctl.PhaseDownloading))
		}
	})

	It("emits the final event after Flush with the latest percentage", func() {
		rec := &emitRecorder{}
		sink := NewDebouncedInstallProgressSink(rec.emit, "n1", "op1", "vllm", 50*time.Millisecond)

		sink.OnDownload("vllm.tar.zst", "1 GB", "1 GB", 100.0)
		sink.Flush()

		Eventually(func() float64 {
			evs := rec.emitted()
			if len(evs) == 0 {
				return -1
			}
			return evs[len(evs)-1].Percentage
		}, "1s").Should(Equal(100.0))
	})
})
