package gallery_test

import (
	"github.com/mudler/LocalAI/core/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	"os"
)

var _ = Describe("Julia native decision gallery", func() {
	It("pins a separately named text-only stock backend artifact", func() {
		data, err := os.ReadFile("../../gallery/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		var entries []gallery.GalleryModel
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		var matches []gallery.GalleryModel
		for _, entry := range entries {
			if entry.Name == "julia-1-llama-cpp" {
				matches = append(matches, entry)
			}
		}
		Expect(matches).To(HaveLen(1))
		entry := matches[0]
		Expect(entry.License).To(Equal("apache-2.0"))
		Expect(entry.Overrides["backend"]).To(Equal("llama-cpp"))
		Expect(entry.Overrides["known_usecases"]).To(ConsistOf("decisions"))
		Expect(entry.AdditionalFiles).To(HaveLen(1))
		Expect(entry.AdditionalFiles[0].URI).To(Equal("https://huggingface.co/ggml-org/Julia-1-GGUF/resolve/16fee17949206fbf58da9347daea44d792a81211/Julia-1-Q8_0.gguf"))
		Expect(entry.AdditionalFiles[0].SHA256).To(Equal("1ea6a7e87156eeeda88cb7a36a61265b37ba7b993897b7289b99aea5b5e47069"))
	})
})
