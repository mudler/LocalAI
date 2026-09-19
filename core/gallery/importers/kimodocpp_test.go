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
			Name      string         `yaml:"name"`
			URLs      []string       `yaml:"urls"`
			Files     []gallery.File `yaml:"files"`
			Tags      []string       `yaml:"tags"`
			Overrides struct {
				Options []string `yaml:"options"`
			} `yaml:"overrides"`
		}
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		count := 0
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name, "kimodo-") {
				continue
			}
			count++
			Expect(entry.URLs).NotTo(BeEmpty())
			quantization := entry.Tags[len(entry.Tags)-1]
			preferences, err := json.Marshal(map[string]string{"text_quantization": quantization})
			Expect(err).NotTo(HaveOccurred())
			model, err := (&importers.KimodoCppImporter{}).Import(importers.Details{URI: entry.URLs[0], Preferences: preferences})
			Expect(err).NotTo(HaveOccurred())
			Expect(model.Name).To(Equal(entry.Name))
			Expect(model.Files).To(Equal(entry.Files), entry.Name)
			for _, option := range entry.Overrides.Options {
				Expect(model.ConfigFile).To(ContainSubstring(option))
			}
		}
		Expect(count).To(Equal(24))
	})
	DescribeTable("imports the complete published model and shared text bundle", func(repo string) {
		details := importers.Details{URI: "https://huggingface.co/LocalAI-io/" + repo, Preferences: json.RawMessage(`{}`)}
		importer := &importers.KimodoCppImporter{}
		Expect(importer.Match(details)).To(BeTrue())
		model, err := importer.Import(details)
		Expect(err).NotTo(HaveOccurred())
		Expect(model.ConfigFile).To(ContainSubstring("backend: kimodocpp"))
		Expect(model.ConfigFile).To(ContainSubstring("FLAG_3D_ANIMATION"))
		Expect(model.Files).To(HaveLen(3))
		Expect(model.ConfigFile).To(ContainSubstring("text_bundle:kimodo/text/Llama-3-Kimodo-Q8_0.gguf"))
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
	It("rejects unknown text quantizations instead of silently downloading a different encoder", func() {
		_, err := (&importers.KimodoCppImporter{}).Import(importers.Details{
			URI: "https://huggingface.co/LocalAI-io/Kimodo-G1-RP-v1-GGML", Preferences: json.RawMessage(`{"text_quantization":"q2_k"}`),
		})
		Expect(err).To(MatchError(ContainSubstring("unsupported text_quantization")))
	})
	It("accepts uppercase quantization preferences", func() {
		model, err := (&importers.KimodoCppImporter{}).Import(importers.Details{
			URI: "https://huggingface.co/LocalAI-io/Kimodo-G1-RP-v1-GGML", Preferences: json.RawMessage(`{"text_quantization":"Q4_K_M"}`),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(model.ConfigFile).To(ContainSubstring("text_bundle:kimodo/text/Llama-3-Kimodo-Q4_K_M.gguf"))
	})
})
