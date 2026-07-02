package galleryop_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The install op must be idempotent unless Force is set: API clients call
// POST /backends/apply on every boot to make sure the backend exists, and an
// unconditional force here re-downloads the whole backend artifact each time.
// Reinstall is an explicit, opted-in action.
var _ = Describe("LocalBackendManager force semantics", func() {
	var (
		backendsDir string
		srcDir      string
		mgr         *galleryop.LocalBackendManager
		systemState *system.SystemState
		ml          *model.ModelLoader
	)

	const installedRunSh = "#!/bin/sh\necho installed\n"
	const galleryRunSh = "#!/bin/sh\necho from-gallery\n"

	installedRunShPath := func() string {
		return filepath.Join(backendsDir, "test-backend", "run.sh")
	}

	BeforeEach(func() {
		var err error
		backendsDir, err = os.MkdirTemp("", "force-backends-*")
		Expect(err).NotTo(HaveOccurred())
		srcDir, err = os.MkdirTemp("", "force-src-*")
		Expect(err).NotTo(HaveOccurred())

		// The gallery serves test-backend from a plain directory (offline).
		// The gallery yaml itself must live under the backends path: file://
		// galleries outside the trusted root are rejected by the downloader.
		Expect(os.WriteFile(filepath.Join(srcDir, "run.sh"), []byte(galleryRunSh), 0o755)).To(Succeed())
		entries := []map[string]any{{"name": "test-backend", "uri": srcDir}}
		data, err := yaml.Marshal(entries)
		Expect(err).NotTo(HaveOccurred())
		galleryYAML := filepath.Join(backendsDir, "gallery.yaml")
		Expect(os.WriteFile(galleryYAML, data, 0o644)).To(Succeed())

		// test-backend is already installed, with content that differs from
		// the gallery's so a reinstall is observable.
		Expect(os.MkdirAll(filepath.Join(backendsDir, "test-backend"), 0o755)).To(Succeed())
		Expect(os.WriteFile(installedRunShPath(), []byte(installedRunSh), 0o755)).To(Succeed())

		systemState, err = system.GetSystemState(system.WithBackendPath(backendsDir))
		Expect(err).NotTo(HaveOccurred())
		appConfig := &config.ApplicationConfig{
			SystemState:      systemState,
			BackendGalleries: []config.Gallery{{Name: "test", URL: "file://" + galleryYAML}},
		}
		ml = model.NewModelLoader(systemState)
		mgr = galleryop.NewLocalBackendManager(appConfig, ml)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(backendsDir)).To(Succeed())
		Expect(os.RemoveAll(srcDir)).To(Succeed())
	})

	It("skips an already-installed backend when Force is not set", func() {
		op := &galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 "op-1",
			GalleryElementName: "test-backend",
		}
		Expect(mgr.InstallBackend(context.Background(), op, nil)).To(Succeed())

		content, err := os.ReadFile(installedRunShPath())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(installedRunSh), "install without Force must not overwrite an installed backend")
	})

	It("reinstalls an already-installed backend when Force is set", func() {
		op := &galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 "op-2",
			GalleryElementName: "test-backend",
			Force:              true,
		}
		Expect(mgr.InstallBackend(context.Background(), op, nil)).To(Succeed())

		content, err := os.ReadFile(installedRunShPath())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(galleryRunSh), "install with Force must overwrite the installed backend")
	})

	// The LOCALAI_EXTERNAL_BACKENDS boot loop goes through
	// InstallExternalBackend's gallery-name path on EVERY startup; it must not
	// force, or each boot re-downloads every listed backend.
	It("skips an already-installed backend on the non-forced external gallery-name path", func() {
		err := galleryop.InstallExternalBackend(context.Background(),
			[]config.Gallery{{Name: "test", URL: "file://" + filepath.Join(backendsDir, "gallery.yaml")}},
			systemState, ml, nil, "test-backend", "", "", false, false)
		Expect(err).NotTo(HaveOccurred())

		content, err := os.ReadFile(installedRunShPath())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(installedRunSh), "non-forced external install must not overwrite an installed backend")
	})
})
