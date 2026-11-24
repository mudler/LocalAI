package galleryop_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

type fakeModelUpgrader struct {
	mu    sync.Mutex
	opts  []galleryop.ModelUpgradeOptions
	names []string
	err   error
}

func (f *fakeModelUpgrader) UpgradeModel(_ context.Context, _, name string, opts galleryop.ModelUpgradeOptions, progress func(*galleryop.OpStatus)) error {
	f.mu.Lock()
	f.opts = append(f.opts, opts)
	f.names = append(f.names, name)
	f.mu.Unlock()
	progress(&galleryop.OpStatus{Phase: gallery.PhaseStaging, Message: "downloading", Cancellable: true})
	return f.err
}

// A model operation with Upgrade set goes to the ModelUpgrader, not to the
// install path, and is reported as an upgrade.
var _ = Describe("A model upgrade operation", func() {
	var (
		svc  *galleryop.GalleryService
		up   *fakeModelUpgrader
		seen chan galleryop.OpResult
	)

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		up = &fakeModelUpgrader{}
		svc.SetModelUpgrader(up)
		seen = make(chan galleryop.OpResult, 2)
		svc.SetOnOpFinished(func(r galleryop.OpResult) { seen <- r })
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())
	})

	It("runs the upgrader with the options and completes", func() {
		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "op-1", GalleryElementName: "tiny", Upgrade: true,
			ModelUpgrade: &galleryop.ModelUpgradeOptions{ConfigOnly: true, Choices: map[string]string{"context_size": "mine"}},
		})
		Eventually(seen).Should(Receive(Equal(galleryop.OpResult{ID: "op-1", Name: "tiny", Upgrade: true, Outcome: galleryop.OpCompleted})))
		st := svc.GetStatus("op-1")
		Expect(st.Upgrade).To(BeTrue())
		Expect(st.Processed).To(BeTrue())
		Expect(up.names).To(Equal([]string{"tiny"}))
		Expect(up.opts[0].ConfigOnly).To(BeTrue())
		Expect(up.opts[0].Choices).To(HaveKeyWithValue("context_size", "mine"))
	})

	It("reports a failed upgrade with its error", func() {
		up.err = errors.New("load failed; the old version was put back")
		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{ID: "op-2", GalleryElementName: "tiny", Upgrade: true})
		Eventually(seen).Should(Receive(HaveField("Outcome", galleryop.OpFailed)))
		Expect(svc.GetStatus("op-2").Error).To(MatchError(ContainSubstring("load failed")))
	})

	It("leaves the exclusion of an upgrade on another frontend to the upgrader, not to the age of an operation record", func() {
		store, err := distributed.NewGalleryStore(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		Expect(store.Create(&distributed.GalleryOperationRecord{ID: "other", GalleryElementName: "tiny", OpType: "model_upgrade", Status: "downloading"})).To(Succeed())
		svc.SetGalleryStore(store)
		up.err = errors.New("an upgrade of this model is already running")
		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{ID: "op-4", GalleryElementName: "tiny", Upgrade: true})
		Eventually(seen).Should(Receive(HaveField("Err", ContainSubstring("already running"))))
		Expect(up.names).To(Equal([]string{"tiny"}), "the upgrader decided, with its model lock")
	})

	It("fails when no upgrader is wired", func() {
		bare := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		got := make(chan galleryop.OpResult, 1)
		bare.SetOnOpFinished(func(r galleryop.OpResult) { got <- r })
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(bare.Start(ctx, nil, nil)).To(Succeed())
		bare.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{ID: "op-3", GalleryElementName: "tiny", Upgrade: true})
		Eventually(got).Should(Receive(HaveField("Err", ContainSubstring("not available"))))
	})
})
