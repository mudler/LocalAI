package galleryop

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type manualArtifactProgressTicker struct {
	channel chan time.Time
	stopped bool
}

func newManualArtifactProgressTicker() *manualArtifactProgressTicker {
	return &manualArtifactProgressTicker{channel: make(chan time.Time, 1)}
}

func (t *manualArtifactProgressTicker) Chan() <-chan time.Time { return t.channel }

func (t *manualArtifactProgressTicker) Stop() { t.stopped = true }

type artifactProgressRecorder struct {
	mu     sync.Mutex
	events []modelartifacts.ProgressEvent
}

func (r *artifactProgressRecorder) Sink(event modelartifacts.ProgressEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *artifactProgressRecorder) Events() []modelartifacts.ProgressEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]modelartifacts.ProgressEvent(nil), r.events...)
}

var _ = Describe("artifact progress coalescer", func() {
	var (
		ticker   *manualArtifactProgressTicker
		recorder *artifactProgressRecorder
	)

	BeforeEach(func() {
		ticker = newManualArtifactProgressTicker()
		recorder = &artifactProgressRecorder{}
		newArtifactProgressTicker = func(time.Duration) artifactProgressTicker {
			return ticker
		}
	})

	AfterEach(func() {
		newArtifactProgressTicker = newRealArtifactProgressTicker
	})

	It("coalesces downloading events and forwards the latest event on a tick", func() {
		coalescer := newArtifactProgressCoalescer(250*time.Millisecond, recorder.Sink)
		DeferCleanup(coalescer.Close)

		coalescer.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseDownloading, CurrentBytes: 32})
		coalescer.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseDownloading, CurrentBytes: 64})
		Expect(recorder.Events()).To(BeEmpty())

		ticker.channel <- time.Now()
		Eventually(recorder.Events).Should(Equal([]modelartifacts.ProgressEvent{{Phase: modelartifacts.PhaseDownloading, CurrentBytes: 64}}))
	})

	It("flushes pending downloading progress before a phase event", func() {
		coalescer := newArtifactProgressCoalescer(250*time.Millisecond, recorder.Sink)
		DeferCleanup(coalescer.Close)

		download := modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseDownloading, CurrentBytes: 64}
		verifying := modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseVerifying, CurrentBytes: 64}
		coalescer.Sink(download)
		coalescer.Sink(verifying)

		Expect(recorder.Events()).To(Equal([]modelartifacts.ProgressEvent{download, verifying}))
	})

	It("flushes pending progress and disables forwarding when closed", func() {
		coalescer := newArtifactProgressCoalescer(250*time.Millisecond, recorder.Sink)
		download := modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseDownloading, CurrentBytes: 64}
		coalescer.Sink(download)

		coalescer.Close()
		Expect(recorder.Events()).To(Equal([]modelartifacts.ProgressEvent{download}))
		Expect(ticker.stopped).To(BeTrue())

		coalescer.Sink(modelartifacts.ProgressEvent{Phase: modelartifacts.PhaseCommitting})
		ticker.channel <- time.Now()
		Consistently(recorder.Events).Should(Equal([]modelartifacts.ProgressEvent{download}))
	})
})
