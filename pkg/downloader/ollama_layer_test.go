package downloader_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/ollamatest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ollama:// layer selector", func() {
	It("downloads the weights by default and the projector with #projector", func() {
		reg := ollamatest.New()
		DeferCleanup(reg.Close)
		DeferCleanup(reg.Use())
		weights := append([]byte("GGUF"), []byte("weights")...)
		projector := append([]byte("GGUF"), []byte("projector")...)
		reg.Add("library/vision:4b", ollamatest.Model{Layers: []ollamatest.Layer{
			{MediaType: oci.OllamaMediaTypeModel, Data: weights},
			{MediaType: oci.OllamaMediaTypeProjector, Data: projector},
		}})

		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "m.gguf")
		Expect(downloader.URI("ollama://vision:4b").DownloadFile(model, "", 1, 1, nil)).To(Succeed())
		Expect(os.ReadFile(model)).To(Equal(weights))

		mm := filepath.Join(dir, "mm.gguf")
		Expect(downloader.URI("ollama://vision:4b#projector").DownloadFile(mm, "", 1, 1, nil)).To(Succeed())
		Expect(os.ReadFile(mm)).To(Equal(projector))
	})
})
