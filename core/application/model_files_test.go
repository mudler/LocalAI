package application

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("declaredModelFiles", func() {
	It("combines the gallery install's files with the config's download_files", func() {
		modelsPath := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(modelsPath, "big.yaml"), []byte(`
name: big
backend: llama-cpp
parameters:
  model: big/Big-00001-of-00002.gguf
download_files:
  - filename: big/extra.bin
    uri: https://example.com/extra.bin
`), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelsPath, gallery.GalleryFileName("big")), []byte(`
files:
  - filename: big/Big-00001-of-00002.gguf
  - filename: big/Big-00002-of-00002.gguf
`), 0o644)).To(Succeed())

		loader := config.NewModelConfigLoader(modelsPath)
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())

		Expect(declaredModelFiles(loader, modelsPath)("big")).To(ConsistOf(
			filepath.Join(modelsPath, "big/Big-00001-of-00002.gguf"),
			filepath.Join(modelsPath, "big/Big-00002-of-00002.gguf"),
			filepath.Join(modelsPath, "big/extra.bin"),
		))
	})
})
