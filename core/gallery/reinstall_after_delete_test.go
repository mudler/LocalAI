package gallery_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// A model is listed as installed while its configuration file exists, so a
// deletion has to remove that file even when something else goes wrong, and it
// has to say so when it could not finish.
var _ = Describe("DeleteModelFromSystem leaves nothing that keeps a model installed", func() {
	var (
		modelsPath string
		state      *system.SystemState
	)

	BeforeEach(func() {
		modelsPath = GinkgoT().TempDir()
		var err error
		state, err = system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).ToNot(HaveOccurred())
	})

	It("removes a configuration that cannot be parsed, with its gallery metadata", func() {
		configPath := filepath.Join(modelsPath, "m.yaml")
		sidecar := filepath.Join(modelsPath, gallery.GalleryFileName("m"))
		Expect(os.WriteFile(configPath, []byte("name: m\nparameters: [broken\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(sidecar, []byte("files: []\n"), 0o644)).To(Succeed())

		err := gallery.DeleteModelFromSystem(state, "m")

		Expect(configPath).ToNot(BeAnExistingFile())
		Expect(sidecar).ToNot(BeAnExistingFile())
		var incomplete *gallery.IncompleteDeleteError
		Expect(errors.As(err, &incomplete)).To(BeTrue(), "the unreadable configuration must be reported")
	})

	It("removes the configuration and reports files it could not remove", func() {
		// A non-empty directory where a file is declared cannot be removed.
		stuck := filepath.Join(modelsPath, "m", "w.gguf")
		Expect(os.MkdirAll(stuck, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(stuck, "keep"), []byte("x"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, "m.yaml"), []byte("name: m\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName("m")), []byte("files:\n  - filename: m/w.gguf\n"), 0o644)).To(Succeed())

		err := gallery.DeleteModelFromSystem(state, "m")

		var incomplete *gallery.IncompleteDeleteError
		Expect(errors.As(err, &incomplete)).To(BeTrue(), "a failed file removal must not be silent")
		Expect(filepath.Join(modelsPath, "m.yaml")).ToNot(BeAnExistingFile())
		Expect(filepath.Join(modelsPath, gallery.GalleryFileName("m"))).ToNot(BeAnExistingFile())
	})

	It("removes nothing when the configuration itself cannot be removed", func() {
		weights := filepath.Join(modelsPath, "w.gguf")
		Expect(os.WriteFile(weights, []byte("w"), 0o644)).To(Succeed())
		// A non-empty directory named like the configuration cannot be removed.
		configDir := filepath.Join(modelsPath, "m.yaml")
		Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(configDir, "x"), []byte("x"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName("m")), []byte("files:\n  - filename: w.gguf\n"), 0o644)).To(Succeed())

		Expect(gallery.DeleteModelFromSystem(state, "m")).ToNot(Succeed())
		Expect(weights).To(BeARegularFile())
	})
})
