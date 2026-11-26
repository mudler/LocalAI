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

	Describe("Strata pack directories", func() {
		writeModel := func(name, dir string) string {
			pack := filepath.Join(modelsPath, dir, "pack")
			Expect(os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(pack, "index.txt"), []byte("i"), 0o644)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(modelsPath, dir, "pack.partial"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelsPath, dir, "w-00001-of-00002.gguf"), []byte("w"), 0o644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelsPath, name+".yaml"), []byte("name: "+name+"\nbackend: strata\nparameters:\n  model: "+dir+"\noptions:\n  - pack_gguf:w-00001-of-00002.gguf\n"), 0o644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName(name)), []byte("files:\n  - filename: "+dir+"/w-00001-of-00002.gguf\n"), 0o644)).To(Succeed())
			return pack
		}

		It("removes the pack the backend built and leaves other models alone", func() {
			packA := writeModel("a", "strata/models/a")
			packB := writeModel("b", "strata/models/b")

			Expect(gallery.DeleteModelFromSystem(state, "a")).To(Succeed())
			Expect(packA).ToNot(BeADirectory())
			Expect(filepath.Join(modelsPath, "strata/models/a/pack.partial")).ToNot(BeADirectory())
			Expect(filepath.Join(modelsPath, "strata/models/a/w-00001-of-00002.gguf")).ToNot(BeAnExistingFile())
			Expect(packB).To(BeADirectory())
			Expect(filepath.Join(packB, "index.txt")).To(BeARegularFile())
			Expect(filepath.Join(modelsPath, "b.yaml")).To(BeARegularFile())
		})

		It("keeps the pack while another model uses the same directory", func() {
			pack := writeModel("a", "strata/models/shared")
			writeModel("b", "strata/models/shared")

			Expect(gallery.DeleteModelFromSystem(state, "a")).To(Succeed())
			Expect(pack).To(BeADirectory())
			Expect(gallery.DeleteModelFromSystem(state, "b")).To(Succeed())
			Expect(pack).ToNot(BeADirectory())
		})

		It("never removes a pack outside the models directory", func() {
			victim := filepath.Join(outside, "pack")
			Expect(os.MkdirAll(victim, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelsPath, "t.yaml"), []byte("name: t\nparameters:\n  model: ../outside\noptions:\n  - pack_gguf:x.gguf\n"), 0o644)).To(Succeed())
			_ = gallery.DeleteModelFromSystem(state, "t")
			Expect(victim).To(BeADirectory())
		})

		It("never removes a pack through a link that leaves the models directory", func() {
			victim := filepath.Join(outside, "pack")
			Expect(os.MkdirAll(victim, 0o755)).To(Succeed())
			Expect(os.Symlink(outside, filepath.Join(modelsPath, "link"))).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelsPath, "l.yaml"), []byte("name: l\nparameters:\n  model: link\noptions:\n  - pack_gguf:x.gguf\n"), 0o644)).To(Succeed())
			_ = gallery.DeleteModelFromSystem(state, "l")
			Expect(victim).To(BeADirectory())
		})

		It("does not touch the models directory when the model path is the models directory", func() {
			shared := filepath.Join(modelsPath, "pack")
			Expect(os.MkdirAll(shared, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelsPath, "r.yaml"), []byte("name: r\nparameters:\n  model: .\noptions:\n  - pack_gguf:x.gguf\n"), 0o644)).To(Succeed())
			_ = gallery.DeleteModelFromSystem(state, "r")
			Expect(shared).To(BeADirectory())
		})

		It("leaves a pack the user pointed at with the pack option", func() {
			pack := writeModel("u", "strata/models/u")
			Expect(os.WriteFile(filepath.Join(modelsPath, "u.yaml"), []byte("name: u\nparameters:\n  model: strata/models/u\noptions:\n  - pack:pack\n  - pack_gguf:w-00001-of-00002.gguf\n"), 0o644)).To(Succeed())
			// The model directory still holds the pack, so the delete reports it.
			_ = gallery.DeleteModelFromSystem(state, "u")
			Expect(pack).To(BeADirectory())
		})
	})
})
