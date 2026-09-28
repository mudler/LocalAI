package gallery_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// DeleteModelFromSystem removes files named by a model name and by the
// model's gallery file. Neither may reach outside the models directory: the
// name can come from an API caller or from an assistant tool call, and the
// gallery file is a YAML file on disk.
var _ = Describe("DeleteModelFromSystem path containment", func() {
	var (
		root       string
		modelsPath string
		outside    string
		state      *system.SystemState
	)

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		modelsPath = filepath.Join(root, "models")
		outside = filepath.Join(root, "outside")
		Expect(os.MkdirAll(modelsPath, 0o755)).To(Succeed())
		Expect(os.MkdirAll(outside, 0o755)).To(Succeed())
		var err error
		state, err = system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).ToNot(HaveOccurred())
	})

	It("refuses a model name that escapes the models directory", func() {
		victim := filepath.Join(outside, "victim.yaml")
		Expect(os.WriteFile(victim, []byte("name: victim\n"), 0o644)).To(Succeed())

		Expect(gallery.DeleteModelFromSystem(state, "../outside/victim")).ToNot(Succeed())
		Expect(victim).To(BeARegularFile())
	})

	It("does not remove gallery-declared files outside the models directory", func() {
		secret := filepath.Join(outside, "secret.bin")
		Expect(os.WriteFile(secret, []byte("x"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, "m.yaml"), []byte("name: m\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName("m")), []byte(`
files:
  - filename: ../outside/secret.bin
`), 0o644)).To(Succeed())

		_ = gallery.DeleteModelFromSystem(state, "m")
		Expect(secret).To(BeARegularFile())
	})

	It("still deletes a normal model and its declared files", func() {
		weights := filepath.Join(modelsPath, "m", "w.gguf")
		Expect(os.MkdirAll(filepath.Dir(weights), 0o755)).To(Succeed())
		Expect(os.WriteFile(weights, []byte("w"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, "m.yaml"), []byte("name: m\nparameters:\n  model: m/w.gguf\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName("m")), []byte(`
files:
  - filename: m/w.gguf
`), 0o644)).To(Succeed())

		Expect(gallery.DeleteModelFromSystem(state, "m")).To(Succeed())
		Expect(weights).ToNot(BeAnExistingFile())
		Expect(filepath.Join(modelsPath, "m.yaml")).ToNot(BeAnExistingFile())
	})
})
