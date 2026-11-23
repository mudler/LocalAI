package galleryop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/system"
)

// failingBackendManager fails every install and upgrade, the way a download
// error does. It never touches the backends directory itself, so what is left
// on disk afterwards is what the handler did.
type failingBackendManager struct{ blockingBackendManager }

var errDownload = errors.New("download failed")

func (failingBackendManager) InstallBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	return errDownload
}

func (failingBackendManager) UpgradeBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	return errDownload
}

// A failed operation must not take an installed backend with it. The handler
// cleans up after a failed first install (a half-written directory), but the
// name of an upgrade is the working build it was meant to replace.
var _ = Describe("backend op failure cleanup", func() {
	var (
		backendsDir string
		svc         *galleryop.GalleryService
	)

	runShPath := func() string { return filepath.Join(backendsDir, "test-backend", "run.sh") }

	runOp := func(id string, upgrade bool) *galleryop.OpStatus {
		svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 id,
			GalleryElementName: "test-backend",
			Upgrade:            upgrade,
		})
		Eventually(func() bool {
			st := svc.GetStatus(id)
			return st != nil && st.Processed
		}, "5s", "20ms").Should(BeTrue())
		return svc.GetStatus(id)
	}

	BeforeEach(func() {
		var err error
		backendsDir, err = os.MkdirTemp("", "failed-op-backends-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(backendsDir)).To(Succeed()) })

		Expect(os.MkdirAll(filepath.Dir(runShPath()), 0o755)).To(Succeed())
		Expect(os.WriteFile(runShPath(), []byte("#!/bin/sh\necho installed\n"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(backendsDir, "test-backend", "metadata.json"), []byte("{}"), 0o644)).To(Succeed())

		systemState, err := system.GetSystemState(system.WithBackendPath(backendsDir))
		Expect(err).NotTo(HaveOccurred())

		svc = galleryop.NewGalleryService(&config.ApplicationConfig{SystemState: systemState}, nil)
		svc.SetBackendManager(failingBackendManager{})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, systemState)).To(Succeed())
	})

	It("keeps the installed backend when its upgrade fails", func() {
		st := runOp("upgrade-fail", true)

		Expect(st.Error).To(MatchError(errDownload))
		Expect(runShPath()).To(BeARegularFile(), "a failed upgrade must leave the installed build runnable")
	})

	It("removes the leftover directory when a plain install fails", func() {
		st := runOp("install-fail", false)

		Expect(st.Error).To(MatchError(errDownload))
		Expect(filepath.Dir(runShPath())).NotTo(BeADirectory())
	})
})
