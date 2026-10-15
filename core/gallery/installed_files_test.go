package gallery_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("InstalledModelFiles", func() {
	var modelsPath string

	BeforeEach(func() {
		modelsPath = GinkgoT().TempDir()
	})

	writeGalleryFile := func(name, body string) {
		Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName(name)), []byte(body), 0o644)).To(Succeed())
	}

	It("returns the files the install declared, under the models path", func() {
		writeGalleryFile("big", `
name: big
files:
  - filename: llama-cpp/models/big/Big-00001-of-00002.gguf
    uri: huggingface://org/repo/Big-00001-of-00002.gguf
  - filename: llama-cpp/models/big/Big-00002-of-00002.gguf
    uri: huggingface://org/repo/Big-00002-of-00002.gguf
`)
		Expect(gallery.InstalledModelFiles(modelsPath, "big")).To(Equal([]string{
			filepath.Join(modelsPath, "llama-cpp/models/big/Big-00001-of-00002.gguf"),
			filepath.Join(modelsPath, "llama-cpp/models/big/Big-00002-of-00002.gguf"),
		}))
	})

	It("drops entries that escape the models path", func() {
		writeGalleryFile("evil", `
files:
  - filename: ../outside.gguf
  - filename: ok.gguf
`)
		Expect(gallery.InstalledModelFiles(modelsPath, "evil")).To(Equal([]string{
			filepath.Join(modelsPath, "ok.gguf"),
		}))
	})

	It("returns nothing for a model that was not installed from a gallery", func() {
		Expect(gallery.InstalledModelFiles(modelsPath, "handwritten")).To(BeEmpty())
	})
})
