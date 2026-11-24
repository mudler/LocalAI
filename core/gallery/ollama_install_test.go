// SPDX-License-Identifier: MIT

package gallery_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/ollamatest"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("installing an Ollama model", func() {
	var (
		reg        *ollamatest.Registry
		modelsPath string
		state      *system.SystemState
	)
	weights := append([]byte("GGUF"), []byte("weights")...)
	projector := append([]byte("GGUF"), []byte("projector")...)

	BeforeEach(func() {
		reg = ollamatest.New()
		DeferCleanup(reg.Close)
		DeferCleanup(reg.Use())
		modelsPath = GinkgoT().TempDir()
		var err error
		state, err = system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
	})

	definition := func() *gallery.ModelConfig {
		return &gallery.ModelConfig{
			Name: "vision-4b",
			Files: []gallery.File{
				{URI: "ollama://vision:4b", Filename: "vision-4b.gguf"},
				{URI: "ollama://vision:4b#projector", Filename: "vision-4b-mmproj.gguf"},
			},
			ConfigFile: "name: vision-4b\nbackend: llama-cpp\nparameters:\n  model: vision-4b.gguf\nmmproj: vision-4b-mmproj.gguf\n",
		}
	}

	It("downloads the weights and the projector layer", func() {
		reg.Add("library/vision:4b", ollamatest.Model{Layers: []ollamatest.Layer{
			{MediaType: oci.OllamaMediaTypeModel, Data: weights},
			{MediaType: oci.OllamaMediaTypeProjector, Data: projector},
		}})
		_, err := gallery.InstallModel(context.Background(), state, "", definition(), nil, nil, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(filepath.Join(modelsPath, "vision-4b.gguf"))).To(Equal(weights))
		Expect(os.ReadFile(filepath.Join(modelsPath, "vision-4b-mmproj.gguf"))).To(Equal(projector))
	})

	It("fails and removes a model file that is not a GGUF file", func() {
		reg.Add("library/vision:4b", ollamatest.Model{Layers: []ollamatest.Layer{
			{MediaType: oci.OllamaMediaTypeModel, Data: []byte("<html>not a model</html>")},
			{MediaType: oci.OllamaMediaTypeProjector, Data: projector},
		}})
		_, err := gallery.InstallModel(context.Background(), state, "", definition(), nil, nil, false)
		Expect(err).To(MatchError(ContainSubstring("this model is not a GGUF file")))
		Expect(filepath.Join(modelsPath, "vision-4b.gguf")).NotTo(BeAnExistingFile())
	})

	It("fails and leaves no file when the blob is corrupted in transit", func() {
		bad := append([]byte(nil), weights...)
		bad[6] ^= 0xff
		reg.Add("library/vision:4b", ollamatest.Model{Layers: []ollamatest.Layer{
			{MediaType: oci.OllamaMediaTypeModel, Data: weights, Served: bad},
			{MediaType: oci.OllamaMediaTypeProjector, Data: projector},
		}})
		_, err := gallery.InstallModel(context.Background(), state, "", definition(), nil, nil, false)
		Expect(err).To(HaveOccurred())
		Expect(filepath.Join(modelsPath, "vision-4b.gguf")).NotTo(BeAnExistingFile())
	})
})
