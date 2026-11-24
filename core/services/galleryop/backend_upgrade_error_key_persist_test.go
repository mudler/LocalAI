package galleryop_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// panickingUpgradeManager panics in every upgrade, like a nil dereference in a
// backend-specific path.
type panickingUpgradeManager struct{ blockingBackendManager }

func (panickingUpgradeManager) UpgradeBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	panic("nil map write")
}

// The code of a failed upgrade is part of the operation record, so a replica
// that reads the row later, or serves the history, still has it.
var _ = Describe("backend upgrade error key persistence", func() {
	var store *distributed.GalleryStore

	BeforeEach(func() {
		var err error
		store, err = distributed.NewGalleryStore(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
	})

	run := func(manager galleryop.BackendManager) *galleryop.GalleryService {
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetBackendManager(manager)
		svc.SetGalleryStore(store)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())

		svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{ID: "up", GalleryElementName: "b", Upgrade: true})
		Eventually(func() bool {
			st := svc.GetStatus("up")
			return st != nil && st.Processed
		}, "5s", "20ms").Should(BeTrue())
		return svc
	}

	It("writes the key of a failed upgrade to the operation record and the history", func() {
		svc := run(scriptedUpgradeManager{err: gallery.WithUpgradeCode(gallery.UpgradeFailureVerify, errors.New("bad signature"))})
		Expect(svc.GetStatus("up").ErrorKey).To(Equal("verify"))

		Eventually(func() string {
			row, err := store.Get("up")
			if err != nil {
				return ""
			}
			return row.ErrorKey
		}, "5s", "50ms").Should(Equal("verify"))

		terminal, err := store.ListTerminal(0)
		Expect(err).ToNot(HaveOccurred())
		Expect(terminal).To(HaveLen(1))
		cache := galleryop.NewOpCache(svc)
		cache.SetGalleryStore(store)
		Expect(cache.History()).To(ContainElement(HaveField("ErrorKey", "verify")))
	})

	It("gives a panic in an upgrade the unknown key", func() {
		svc := run(panickingUpgradeManager{})
		Expect(svc.GetStatus("up").ErrorKey).To(Equal("unknown"))
		Eventually(func() string {
			row, err := store.Get("up")
			if err != nil {
				return ""
			}
			return row.ErrorKey
		}, "5s", "50ms").Should(Equal("unknown"))
	})

	It("writes a rollback to the operation record and the history", func() {
		svc := run(scriptedUpgradeManager{err: &gallery.UpgradeError{Code: gallery.UpgradeFailureStart, Err: errors.New("did not start"), RolledBack: true}})
		Expect(svc.GetStatus("up").RolledBack).To(BeTrue())

		Eventually(func() bool {
			row, err := store.Get("up")
			return err == nil && row.RolledBack && row.ErrorKey == "start"
		}, "5s", "50ms").Should(BeTrue())
		cache := galleryop.NewOpCache(svc)
		cache.SetGalleryStore(store)
		Expect(cache.History()).To(ContainElement(And(HaveField("ErrorKey", "start"), HaveField("RolledBack", true))))
	})

	It("restores the rollback and the unconfirmed flag when a replica hydrates the row", func() {
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "rb", GalleryElementName: "b", OpType: "backend_upgrade", Status: "processing",
			Error: "did not start", ErrorKey: "start", RolledBack: true,
		})).To(Succeed())
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "lost", GalleryElementName: "c", OpType: "backend_upgrade", Status: "processing", Unconfirmed: true,
		})).To(Succeed())

		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())

		Expect(fresh.GetStatus("rb")).To(PointTo(And(HaveField("ErrorKey", "start"), HaveField("RolledBack", true))))
		Expect(fresh.GetStatus("lost")).To(PointTo(HaveField("Unconfirmed", true)))
	})

	It("restores the key when a replica hydrates the row", func() {
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "hyd", GalleryElementName: "b", OpType: "backend_upgrade", Status: "processing",
			Error: "boom", ErrorKey: "swap",
		})).To(Succeed())

		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())

		st := fresh.GetStatus("hyd")
		Expect(st).ToNot(BeNil())
		Expect(st.ErrorKey).To(Equal("swap"))
	})
})
