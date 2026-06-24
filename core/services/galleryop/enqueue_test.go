package galleryop_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// These specs reproduce the "install endpoint lies about success" bug observed
// on a 2-replica distributed cluster: POST /models/apply answered HTTP 200 with
// a fresh job UUID, but GET /models/jobs/<uuid> answered HTTP 500 "could not
// find any status for ID" and GET /models/jobs never listed the job.
//
// The mechanism is that the admission handlers mint the UUID, hand the op to an
// unbuffered channel from a detached goroutine, and return 200 immediately. The
// worker is strictly serial, so while a long install is in flight the op sits in
// a blocked send and NOTHING has written a status for it — the first status
// write happens inside modelHandler, i.e. only once the worker actually starts
// the work. A job that is queued behind a running install is therefore
// indistinguishable, over the API, from a job ID that was never issued.
var _ = Describe("GalleryService operation admission", func() {
	var svc *galleryop.GalleryService

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
	})

	Context("when the worker is busy and cannot accept the op yet", func() {
		It("still makes the model job queryable straight away", func() {
			// No consumer is running: this is exactly the state of the channel
			// while the worker is mid-download on a previous op.
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-queued-model",
				GalleryElementName: "localai@longcat-video-avatar-1.5",
			})

			Eventually(func() *galleryop.OpStatus {
				return svc.GetStatus("job-queued-model")
			}, "2s", "10ms").ShouldNot(BeNil(), "a job ID handed to the client must have a status the client can poll")

			Expect(svc.GetAllStatus()).To(HaveKey("job-queued-model"))
			st := svc.GetStatus("job-queued-model")
			Expect(st.Processed).To(BeFalse())
			Expect(st.GalleryElementName).To(Equal("localai@longcat-video-avatar-1.5"))
		})

		It("still makes the backend job queryable straight away", func() {
			svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{
				ID:                 "job-queued-backend",
				GalleryElementName: "llama-cpp",
			})

			Eventually(func() *galleryop.OpStatus {
				return svc.GetStatus("job-queued-backend")
			}, "2s", "10ms").ShouldNot(BeNil())
		})
	})

	Context("when the op is abandoned before the worker ever accepts it", func() {
		It("turns the queued job into a terminal failure instead of leaking silently", func() {
			ctx, cancel := context.WithCancel(context.Background())
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-abandoned",
				GalleryElementName: "localai@some-model",
				Context:            ctx,
				CancelFunc:         cancel,
			})
			cancel()

			Eventually(func() bool {
				st := svc.GetStatus("job-abandoned")
				return st != nil && st.Processed
			}, "2s", "10ms").Should(BeTrue(), "an op that never reached the worker must not stay 'queued' forever")
		})
	})

	Context("when the worker is draining the channel", func() {
		It("delivers the op to the worker", func() {
			received := make(chan string, 1)
			go func() {
				op := <-svc.ModelGalleryChannel
				received <- op.ID
			}()

			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-delivered",
				GalleryElementName: "localai@some-model",
			})

			Eventually(received, "2s").Should(Receive(Equal("job-delivered")))
		})
	})
})

// This spec covers the orphaned-op half of the report: a controller replaced
// mid-download left an op reporting phase=downloading / processed=false /
// error=none indefinitely while nothing was downloading. The PostgreSQL-side
// duplicate guard does time out (FindDuplicate ignores rows not updated for 30
// minutes, and CleanStale marks them failed), but the reaper only ever touched
// the database — the in-memory statuses map that GET /models/jobs/<id> and
// /api/operations actually read was never corrected, so every replica kept
// serving the frozen "downloading" status forever.
var _ = Describe("GalleryService.ReapStaleOperations", func() {
	It("marks the reaped operation failed in memory, not just in the store", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetGalleryStore(store)

		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "orphaned-op",
			GalleryElementName: "localai@longcat-video-avatar-1.5",
			OpType:             "model_install",
			Status:             "downloading",
			Cancellable:        true,
		})).To(Succeed())

		// The in-memory view the API serves: frozen mid-download.
		svc.UpdateStatus("orphaned-op", &galleryop.OpStatus{
			Message:            "downloading",
			Phase:              "downloading",
			Progress:           13.8,
			Cancellable:        true,
			GalleryElementName: "localai@longcat-video-avatar-1.5",
		})

		// Age the row past the reap horizon. Raw SQL so gorm's autoUpdateTime
		// does not stamp updated_at back to now.
		Expect(db.Exec("UPDATE gallery_operations SET updated_at = ? WHERE id = ?",
			time.Now().Add(-2*time.Hour), "orphaned-op").Error).To(Succeed())

		n, err := svc.ReapStaleOperations(30 * time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeNumerically("==", 1))

		st := svc.GetStatus("orphaned-op")
		Expect(st).ToNot(BeNil())
		Expect(st.Processed).To(BeTrue(), "a reaped op must stop claiming it is still downloading")
		Expect(st.Error).To(HaveOccurred())
		Expect(st.Cancellable).To(BeFalse())
	})
})

// The worker is a single goroutine consuming both gallery channels serially,
// so anything that takes it down takes every queued operation with it. A panic
// inside one install handler used to propagate out of that goroutine and kill
// the whole process; contained, it must fail only the operation that caused it
// and leave the consumer able to pick up the next one.
type panickingModelManager struct{}

func (panickingModelManager) InstallModel(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], _ galleryop.ProgressCallback) error {
	panic("boom: malformed gallery entry")
}

func (panickingModelManager) DeleteModel(string) error { return nil }

var _ = Describe("GalleryService worker resilience", func() {
	It("keeps consuming operations after a handler panics", func() {
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetModelManager(panickingModelManager{})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		Expect(svc.Start(ctx, nil, nil)).To(Succeed())

		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 "job-panics",
			GalleryElementName: "localai@exploding-entry",
		})

		Eventually(func() bool {
			st := svc.GetStatus("job-panics")
			return st != nil && st.Processed && st.Error != nil
		}, "5s", "20ms").Should(BeTrue(), "the panicking op must be reported as failed")

		// The consumer must still be alive for the next operation.
		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 "job-after-panic",
			GalleryElementName: "localai@another-entry",
		})

		Eventually(func() bool {
			st := svc.GetStatus("job-after-panic")
			return st != nil && st.Processed && st.Error != nil
		}, "5s", "20ms").Should(BeTrue(), "a later op must still be picked up by the worker")
	})
})
