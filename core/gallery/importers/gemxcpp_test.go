// SPDX-License-Identifier: MIT
package importers_test

import (
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"
	"os"
)

var _ = Describe("GemXCppImporter", func() {
	It("detects the published bundle without capturing unrelated GGUFs", func() {
		i := &importers.GemXCppImporter{}
		Expect(i.Match(importers.Details{URI: "https://huggingface.co/LocalAI-io/GEM-X-GGUF"})).To(BeTrue())
		Expect(i.Match(importers.Details{URI: "https://huggingface.co/other/llama-GGUF"})).To(BeFalse())
		Expect(i.Match(importers.Details{URI: "https://huggingface.co/LocalAI-io/GEM-X-GGUF", Preferences: []byte(`{"backend":"llama-cpp"}`)})).To(BeFalse())
		Expect(i.Match(importers.Details{URI: "unknown", Preferences: []byte(`{"backend":"gemxcpp"}`)})).To(BeTrue())
		_, err := i.Import(importers.Details{URI: "unknown"})
		Expect(err).To(HaveOccurred())
	})
	It("keeps gallery and importer pinned component inventories identical", func() {
		model, err := (&importers.GemXCppImporter{}).Import(importers.Details{URI: "https://huggingface.co/LocalAI-io/GEM-X-GGUF", Preferences: []byte(`{"name":"custom-motion"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(model.Name).To(Equal("custom-motion"))
		Expect(model.ConfigFile).To(ContainSubstring("gemxcpp"))
		Expect(model.Files).To(HaveLen(3))
		data, err := os.ReadFile("../../../gallery/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		var entries []struct {
			Name  string         `yaml:"name"`
			Files []gallery.File `yaml:"files"`
		}
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		found := false
		for _, e := range entries {
			if e.Name == "gem-x" {
				found = true
				Expect(e.Files).To(Equal(model.Files))
			}
		}
		Expect(found).To(BeTrue())
	})
})
