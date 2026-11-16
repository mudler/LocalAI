// SPDX-License-Identifier: MIT
package importers_test

import (
	"encoding/json"
	"os"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"
)

var _ = Describe("SkinTokensCppImporter", func() {
	DescribeTable("detects only the published bundle unless explicitly selected", func(uri, preferences string, matches bool) {
		Expect((&importers.SkinTokensCppImporter{}).Match(importers.Details{URI: uri, Preferences: json.RawMessage(preferences)})).To(Equal(matches))
	},
		Entry("published repo", "https://huggingface.co/LocalAI-io/SkinTokens-GGUF", `{}`, true),
		Entry("backend override wins", "https://huggingface.co/LocalAI-io/SkinTokens-GGUF", `{"backend":"llama-cpp"}`, false),
		Entry("unrelated GGUF", "https://huggingface.co/Qwen/Qwen3-0.6B-GGUF", `{}`, false),
		Entry("explicit selection", "https://huggingface.co/LocalAI-io/SkinTokens-GGUF", `{"backend":"skintokenscpp"}`, true),
		Entry("invalid preferences", "https://huggingface.co/LocalAI-io/SkinTokens-GGUF", `{`, false),
	)
	It("imports a complete revision-pinned F16 bundle and preserves the requested name", func() {
		model, err := (&importers.SkinTokensCppImporter{}).Import(importers.Details{URI: "https://huggingface.co/LocalAI-io/SkinTokens-GGUF", Preferences: json.RawMessage(`{"name":"character-rig"}`)})
		Expect(err).NotTo(HaveOccurred())
		Expect(model.Name).To(Equal("character-rig"))
		Expect(model.ConfigFile).To(ContainSubstring("backend: skintokenscpp"))
		Expect(model.ConfigFile).To(ContainSubstring("model: skintokens/F16"))
		Expect(model.Files).To(HaveLen(3))
		for _, file := range model.Files {
			Expect(file.Filename).To(HavePrefix("skintokens/F16/"))
			Expect(file.SHA256).To(HaveLen(64))
			Expect(file.URI).To(ContainSubstring("/resolve/2c55d38ffe01871c6956103926cc4a40bd7acc8a/"))
		}
	})
	It("does not claim to import arbitrary bundles with unknown layouts", func() {
		_, err := (&importers.SkinTokensCppImporter{}).Import(importers.Details{URI: "https://huggingface.co/example/unknown", Preferences: json.RawMessage(`{"backend":"skintokenscpp"}`)})
		Expect(err).To(HaveOccurred())
	})
	It("keeps the model gallery and importer file inventories identical", func() {
		data, err := os.ReadFile("../../../gallery/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		var entries []struct {
			Name  string         `yaml:"name"`
			Files []gallery.File `yaml:"files"`
		}
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		model, err := (&importers.SkinTokensCppImporter{}).Import(importers.Details{URI: "https://huggingface.co/LocalAI-io/SkinTokens-GGUF"})
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, entry := range entries {
			if entry.Name == "skintokens-f16" {
				Expect(entry.Files).To(Equal(model.Files))
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})
})
