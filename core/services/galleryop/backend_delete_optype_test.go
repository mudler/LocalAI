package galleryop_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// blockingBackendManager holds an operation open so the test can read the row
// as it stands while the operation is in flight. That is the only window the
// cancellable column means anything: the terminal write clears it either way.
type blockingBackendManager struct{ release chan struct{} }

func (b blockingBackendManager) InstallBackend(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	<-b.release
	return nil
}

func (b blockingBackendManager) DeleteBackend(string) error {
	<-b.release
	return nil
}

func (blockingBackendManager) ListBackends() (gallery.SystemBackends, error) {
	return gallery.SystemBackends{}, nil
}

func (b blockingBackendManager) UpgradeBackend(_ context.Context, _ *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	<-b.release
	return nil
}

func (blockingBackendManager) CheckUpgrades(_ context.Context) (map[string]gallery.UpgradeInfo, error) {
	return nil, nil
}

func (blockingBackendManager) IsDistributed() bool { return false }

// What the backend channel persists about a removal. The row is all a replica
// that restarts mid-operation has to go on, so a removal recorded as an install
// is a removal that renders as "Installing backend X" and offers a Cancel
// button that does not apply to it.
var _ = Describe("backend removal persistence", func() {
	var (
		svc     *galleryop.GalleryService
		store   *distributed.GalleryStore
		release chan struct{}
	)

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		store, err = distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		release = make(chan struct{})
		DeferCleanup(func() { close(release) })

		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetBackendManager(blockingBackendManager{release: release})
		svc.SetGalleryStore(store)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())
	})

	// inFlight waits for the row the consumer writes before it enters the
	// handler, and returns it.
	inFlight := func(id string) *distributed.GalleryOperationRecord {
		var rec *distributed.GalleryOperationRecord
		Eventually(func() error {
			op, err := store.Get(id)
			rec = op
			return err
		}, "5s", "20ms").Should(Succeed())
		return rec
	}

	It("records a removal as a removal", func() {
		svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 "job-del",
			GalleryElementName: "vllm",
			Delete:             true,
		})

		Expect(inFlight("job-del").OpType).To(Equal("backend_delete"))
	})

	It("still records an install as an install", func() {
		svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 "job-install",
			GalleryElementName: "vllm",
		})

		Expect(inFlight("job-install").OpType).To(Equal("backend_install"))
	})

	// Hydrate is the only reader that decides from op_type whether an operation
	// is a removal, and it is what a replica restarting mid-operation runs.
	It("hydrates a backend removal as a removal", func() {
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "job-hydrate", GalleryElementName: "vllm",
			OpType: "backend_delete", Status: "processing",
		})).To(Succeed())

		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())

		st := fresh.GetStatus("job-hydrate")
		Expect(st).ToNot(BeNil())
		Expect(st.Deletion).To(BeTrue(),
			"a hydrated backend removal reported as an install renders as 'Installing backend vllm'")
	})

	It("still hydrates a model removal as a removal", func() {
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "job-hydrate", GalleryElementName: "localai@qwen3-4b",
			OpType: "model_delete", Status: "processing",
		})).To(Succeed())

		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())

		Expect(fresh.GetStatus("job-hydrate").Deletion).To(BeTrue())
	})

	It("does not report an install as a removal", func() {
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "job-hydrate", GalleryElementName: "vllm",
			OpType: "backend_install", Status: "processing",
		})).To(Succeed())

		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())

		Expect(fresh.GetStatus("job-hydrate").Deletion).To(BeFalse())
	})
})
