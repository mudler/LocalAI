package galleryop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/system"
)

// scriptedUpgradeManager returns the error it is given from every upgrade and
// install.
type scriptedUpgradeManager struct {
	blockingBackendManager
	err error
}

func (m scriptedUpgradeManager) UpgradeBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	return m.err
}

func (m scriptedUpgradeManager) InstallBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	return m.err
}

// cancellingUpgradeManager cancels the operation, as a user does while the
// upgrade rolls back, and returns err.
type cancellingUpgradeManager struct {
	blockingBackendManager
	cancel context.CancelFunc
	err    error
}

func (m cancellingUpgradeManager) UpgradeBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	m.cancel()
	return m.err
}

var _ = Describe("backend upgrade error key", func() {
	runWith := func(manager scriptedUpgradeManager, upgrade bool) *galleryop.OpStatus {
		dir, err := os.MkdirTemp("", "error-key-backends-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		systemState, err := system.GetSystemState(system.WithBackendPath(dir))
		Expect(err).NotTo(HaveOccurred())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{SystemState: systemState}, nil)
		svc.SetBackendManager(manager)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, systemState)).To(Succeed())

		svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{ID: "op", GalleryElementName: "b", Upgrade: upgrade})
		Eventually(func() bool {
			st := svc.GetStatus("op")
			return st != nil && st.Processed
		}, "5s", "20ms").Should(BeTrue())
		return svc.GetStatus("op")
	}

	It("carries the code of the step that failed", func() {
		for _, code := range []gallery.UpgradeFailureCode{
			gallery.UpgradeFailureDownload, gallery.UpgradeFailureVerify, gallery.UpgradeFailureSwap,
			gallery.UpgradeFailureNotInstalled, gallery.UpgradeFailureNoUpdate, gallery.UpgradeFailureStale,
		} {
			st := runWith(scriptedUpgradeManager{err: fmt.Errorf("step: %w", gallery.WithUpgradeCode(code, errors.New("boom")))}, true)
			Expect(st.ErrorKey).To(Equal(string(code)))
			Expect(st.Error).To(HaveOccurred())
		}
	})

	It("gives a failed upgrade without a code the unknown code", func() {
		st := runWith(scriptedUpgradeManager{err: errors.New("raw failure")}, true)
		Expect(st.ErrorKey).To(Equal("unknown"))
		Expect(st.Error).To(MatchError("raw failure"))
	})

	It("reports a failed restore after a cancel as a failure, not as cancelled", func() {
		dir, err := os.MkdirTemp("", "error-key-backends-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		systemState, err := system.GetSystemState(system.WithBackendPath(dir))
		Expect(err).NotTo(HaveOccurred())
		opCtx, opCancel := context.WithCancel(context.Background())
		DeferCleanup(opCancel)
		svc := galleryop.NewGalleryService(&config.ApplicationConfig{SystemState: systemState}, nil)
		svc.SetBackendManager(cancellingUpgradeManager{cancel: opCancel,
			err: &gallery.UpgradeError{Code: gallery.UpgradeFailureStart, Err: errors.Join(context.Canceled, gallery.ErrUpgradeRestore)}})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, systemState)).To(Succeed())
		svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{ID: "op", GalleryElementName: "b", Upgrade: true, Context: opCtx})
		Eventually(func() bool {
			st := svc.GetStatus("op")
			return st != nil && st.Processed
		}, "5s", "20ms").Should(BeTrue())
		st := svc.GetStatus("op")
		Expect(st.Cancelled).To(BeFalse())
		Expect(st.Error).To(HaveOccurred())
		Expect(errors.Is(st.Error, gallery.ErrUpgradeRestore)).To(BeTrue())
		Expect(st.ErrorKey).To(Equal(string(gallery.UpgradeFailureStart)))
	})

	It("leaves the key empty for a failed install", func() {
		st := runWith(scriptedUpgradeManager{err: errors.New("raw failure")}, false)
		Expect(st.ErrorKey).To(BeEmpty())
	})

	It("keeps the key across the wire", func() {
		data, err := json.Marshal(galleryop.OpStatus{Error: errors.New("boom"), ErrorKey: "verify", Processed: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"error_key":"verify"`))
		var back galleryop.OpStatus
		Expect(json.Unmarshal(data, &back)).To(Succeed())
		Expect(back.ErrorKey).To(Equal("verify"))
	})
})
