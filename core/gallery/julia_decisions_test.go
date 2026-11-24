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

var _ = Describe("native decision family defaults", func() {
	It("pins all six defaults and enables only OpenJev with its exact projector", func() {
		data, err := os.ReadFile("../../gallery/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		var entries []gallery.GalleryModel
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		expected := map[string]string{"julia-1-llama-cpp": "apache-2.0", "laya-llama-cpp": "apache-2.0", "kev-4b-llama-cpp": "apache-2.0", "lev-llama-cpp": "apache-2.0", "openjev-llama-cpp": "cc-by-nc-4.0", "nimble-9b-v3-llama-cpp": "cc-by-nc-4.0"}
		found := map[string]int{}
		for _, entry := range entries {
			license, ok := expected[entry.Name]
			if !ok {
				continue
			}
			found[entry.Name]++
			Expect(entry.License).To(Equal(license))
			Expect(entry.Overrides["backend"]).To(Equal("llama-cpp"))
			Expect(entry.Overrides["known_usecases"]).To(ConsistOf("decisions"))
			if entry.Name == "openjev-llama-cpp" {
				Expect(entry.AdditionalFiles).To(HaveLen(2))
				Expect(entry.Overrides["mmproj"]).To(Equal("mmproj-OpenJev-Q8_0.gguf"))
				Expect(entry.Overrides["context_size"]).To(Equal(8192))
				Expect(entry.AdditionalFiles[1].URI).To(Equal("https://huggingface.co/ggml-org/OpenJev-GGUF/resolve/10840f375658dea7afc5ff4711127bca8218b560/mmproj-OpenJev-Q8_0.gguf"))
				Expect(entry.AdditionalFiles[1].SHA256).To(Equal("e372cdbf59fdd6bd2504cb64c988b31c7a42ac406a8f711df4b7a7acd9216f1e"))
			} else {
				Expect(entry.AdditionalFiles).To(HaveLen(1))
				Expect(entry.Overrides).NotTo(HaveKey("mmproj"))
			}
			Expect(entry.AdditionalFiles[0].URI).To(MatchRegexp(`^https://huggingface.co/ggml-org/[^/]+/resolve/[0-9a-f]{40}/[^/]+\.gguf$`))
			Expect(entry.AdditionalFiles[0].SHA256).To(MatchRegexp(`^[0-9a-f]{64}$`))
			Expect(entry.Tags).NotTo(ContainElement("vision"))
		}
		for name := range expected {
			Expect(found[name]).To(Equal(1), name)
		}
	})
})
