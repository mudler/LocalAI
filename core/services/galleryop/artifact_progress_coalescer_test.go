package galleryop

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
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

type modelOperationProgressManager struct {
	err error
}

func (m *modelOperationProgressManager) InstallModel(ctx context.Context, _ *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], _ ProgressCallback) error {
	modelartifacts.ReportProgress(ctx, modelartifacts.ProgressEvent{
		Phase: modelartifacts.PhaseDownloading, File: "model.bin", CurrentBytes: 32, TotalBytes: 64,
	})
	modelartifacts.ReportProgress(ctx, modelartifacts.ProgressEvent{
		Phase: modelartifacts.PhaseDownloading, File: "model.bin", CurrentBytes: 64, TotalBytes: 64,
	})
	return m.err
}

func (m *modelOperationProgressManager) DeleteModel(string) error { return nil }

type legacyModelOperationProgressManager struct {
	err error
}

func (m *legacyModelOperationProgressManager) InstallModel(_ context.Context, _ *ManagementOp[gallery.GalleryModel, gallery.ModelConfig], progress ProgressCallback) error {
	progress("model.bin", "25 B", "100 B", 25)
	progress("model.bin", "50 B", "100 B", 50)
	return m.err
}

func (m *legacyModelOperationProgressManager) DeleteModel(string) error { return nil }

type recordingProgressClient struct {
	mu      sync.Mutex
	updates []*OpStatus
}

func (c *recordingProgressClient) Publish(_ string, data any) error {
	event, ok := data.(GalleryProgressEvent)
	if !ok || event.Status == nil || event.Status.Phase != string(modelartifacts.PhaseDownloading) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, event.Status)
	return nil
}

func (c *recordingProgressClient) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}

func (c *recordingProgressClient) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}

func (c *recordingProgressClient) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, nil
}

func (c *recordingProgressClient) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, nil
}

func (c *recordingProgressClient) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}

func (c *recordingProgressClient) IsConnected() bool { return true }

func (c *recordingProgressClient) Close() {}

func (c *recordingProgressClient) Updates() []*OpStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*OpStatus(nil), c.updates...)
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

	It("coalesces model operation progress through the existing bridge", func() {
		installErr := errors.New("stop after progress")
		progressClient := &recordingProgressClient{}
		service := NewGalleryService(&config.ApplicationConfig{}, nil)
		service.modelManager = &modelOperationProgressManager{err: installErr}
		service.natsClient = progressClient
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 "model-operation",
			GalleryElementName: "model",
			Context:            context.Background(),
		}

		Expect(service.modelHandler(op, nil, nil)).To(MatchError(installErr))
		Expect(progressClient.Updates()).To(ConsistOf(&OpStatus{
			Phase:              string(modelartifacts.PhaseDownloading),
			Message:            "Downloading model file: model.bin",
			FileName:           "model.bin",
			Progress:           90,
			CurrentBytes:       64,
			TotalBytes:         64,
			GalleryElementName: "model",
			Cancellable:        true,
		}))
	})

	It("coalesces legacy callback progress and flushes numeric bytes on close", func() {
		installErr := errors.New("stop after legacy progress")
		progressClient := &recordingProgressClient{}
		service := NewGalleryService(&config.ApplicationConfig{}, nil)
		service.modelManager = &legacyModelOperationProgressManager{err: installErr}
		service.natsClient = progressClient
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 "legacy-model-operation",
			GalleryElementName: "model",
			Context:            context.Background(),
		}

		Expect(service.modelHandler(op, nil, nil)).To(MatchError(installErr))
		status := service.GetStatus(op.ID)
		Expect(status).NotTo(BeNil())
		Expect(status.Progress).To(Equal(float64(50)))
		Expect(status.CurrentBytes).To(Equal(int64(50)))
		Expect(status.TotalBytes).To(Equal(int64(100)))
		Expect(status.DownloadedFileSize).To(Equal("50 B"))
	})

	It("forwards only the latest legacy callback update on each tick", func() {
		updates := make(chan legacyProgressUpdate, 2)
		coalescer := newLegacyProgressCoalescer(250*time.Millisecond, func(update legacyProgressUpdate) {
			updates <- update
		})
		DeferCleanup(coalescer.Close)

		coalescer.Sink("model.bin", "25 B", "100 B", 25)
		coalescer.Sink("model.bin", "50 B", "100 B", 50)
		Consistently(updates).ShouldNot(Receive())

		ticker.channel <- time.Now()
		Eventually(updates).Should(Receive(Equal(legacyProgressUpdate{
			fileName: "model.bin", current: "50 B", total: "100 B", percentage: 50,
		})))
	})
})
