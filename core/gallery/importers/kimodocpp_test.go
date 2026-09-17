// SPDX-License-Identifier: MIT
package importers_test

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"
)

var _ = Describe("KimodoCppImporter", func() {
	It("keeps the gallery and importer download inventories identical", func() {
		data, err := os.ReadFile("../../../gallery/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		var entries []struct {
			Name  string         `yaml:"name"`
			URLs  []string       `yaml:"urls"`
			Files []gallery.File `yaml:"files"`
		}
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		count := 0
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name, "kimodo-") {
				continue
			}
			count++
			Expect(entry.URLs).NotTo(BeEmpty())
			model, err := (&importers.KimodoCppImporter{}).Import(importers.Details{URI: entry.URLs[0]})
			Expect(err).NotTo(HaveOccurred())
			Expect(model.Files).To(Equal(entry.Files), entry.Name)
		}
		Expect(count).To(Equal(4))
	})
	DescribeTable("imports the complete published model and shared text bundle", func(repo string) {
		details := importers.Details{URI: "https://huggingface.co/LocalAI-io/" + repo, Preferences: json.RawMessage(`{}`)}
		importer := &importers.KimodoCppImporter{}
		Expect(importer.Match(details)).To(BeTrue())
		model, err := importer.Import(details)
		Expect(err).NotTo(HaveOccurred())
		Expect(model.ConfigFile).To(ContainSubstring("backend: kimodocpp"))
		Expect(model.ConfigFile).To(ContainSubstring("FLAG_3D_ANIMATION"))
		Expect(model.Files).To(HaveLen(36))
		for _, file := range model.Files {
			Expect(file.SHA256).To(HaveLen(64))
			Expect(file.URI).NotTo(ContainSubstring("/resolve/main/"))
			Expect(file.Filename).To(HavePrefix("kimodo/"))
		}
	}, Entry("SOMA RP", "Kimodo-SOMA-RP-v1.1-GGML"), Entry("SOMA SEED", "Kimodo-SOMA-SEED-v1.1-GGML"), Entry("G1 RP", "Kimodo-G1-RP-v1-GGML"), Entry("G1 SEED", "Kimodo-G1-SEED-v1-GGML"))
	It("respects explicit backend and model-name preferences", func() {
		importer := &importers.KimodoCppImporter{}
		details := importers.Details{URI: "https://huggingface.co/LocalAI-io/Kimodo-G1-RP-v1-GGML", Preferences: json.RawMessage(`{"backend":"llama-cpp"}`)}
		Expect(importer.Match(details)).To(BeFalse())
		details.Preferences = json.RawMessage(`{"backend":"kimodocpp","name":"robot-motion"}`)
		Expect(importer.Match(details)).To(BeTrue())
		model, err := importer.Import(details)
		Expect(err).NotTo(HaveOccurred())
		Expect(model.Name).To(Equal("robot-motion"))
	})
	It("does not classify arbitrary GGUF models as motion models", func() {
		importer := &importers.KimodoCppImporter{}
		Expect(importer.Match(importers.Details{URI: "https://huggingface.co/example/model/resolve/main/model.gguf"})).To(BeFalse())
	})
})
