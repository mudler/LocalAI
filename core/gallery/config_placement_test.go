// SPDX-License-Identifier: MIT

package gallery_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// These ModelConfig fields are not PredictionOptions. The permissive loader
// silently ignores them when a template or override nests them in parameters.
func misplacedRootOptions(cfg map[string]any, source string) []string {
	params, _ := cfg["parameters"].(map[string]any)
	var violations []string
	for _, key := range []string{"context_size", "f16", "mmap"} {
		if _, exists := params[key]; exists {
			violations = append(violations, fmt.Sprintf("%s: parameters.%s belongs at the root", source, key))
		}
	}
	return violations
}

var _ = Describe("gallery config placement", func() {
	It("keeps known root options out of qwen3 template parameters", func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "qwen3.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var definition gallery.ModelConfig
		Expect(yaml.Unmarshal(data, &definition)).To(Succeed())
		var cfg map[string]any
		Expect(yaml.Unmarshal([]byte(definition.ConfigFile), &cfg)).To(Succeed())
		Expect(misplacedRootOptions(cfg, "qwen3.yaml")).To(BeEmpty())
	})

	It("keeps known root options out of index override parameters", func() {
		entries, err := loadGalleryIndex()
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty())
		var violations []string
		for _, entry := range entries {
			violations = append(violations, misplacedRootOptions(entry.Overrides, entry.Name)...)
		}
		Expect(violations).To(BeEmpty(), strings.Join(violations, "\n"))
	})

	DescribeTable("preserves model-specific context overrides", func(name string, contextSize int) {
		entries, err := loadGalleryIndex()
		Expect(err).NotTo(HaveOccurred())
		entry, found := indexEntriesByName(entries)[name]
		Expect(found).To(BeTrue())
		Expect(entry.Overrides).To(HaveKeyWithValue("context_size", contextSize))
		data, err := yaml.Marshal(entry.Overrides)
		Expect(err).NotTo(HaveOccurred())
		var loaded config.ModelConfig
		Expect(yaml.Unmarshal(data, &loaded)).To(Succeed())
		Expect(loaded.ContextSize).To(HaveValue(Equal(contextSize)))
		Expect(loaded.Model).NotTo(BeEmpty())
	},
		Entry("Supra2", "supra2-100m-instruct", 2048),
		Entry("Shieldstral Q4", "shieldstral-1.0-3b", 32768),
		Entry("Shieldstral Q8", "shieldstral-1.0-3b-q8", 32768),
	)

	It("installs qwen3-vl-4b-thinking with root options and nested model", func() {
		entries, err := loadGalleryIndex()
		Expect(err).NotTo(HaveOccurred())
		entry, found := indexEntriesByName(entries)["qwen3-vl-4b-thinking"]
		Expect(found).To(BeTrue())
		Expect(entry.URL).To(Equal("github:mudler/LocalAI/gallery/qwen3.yaml@master"))
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "qwen3.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var definition gallery.ModelConfig
		Expect(yaml.Unmarshal(data, &definition)).To(Succeed())

		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		// Stand in for downloaded weights without changing the real overrides or
		// template. Existing files with no checksum need no network access.
		Expect(entry.AdditionalFiles).NotTo(BeEmpty())
		for _, file := range entry.AdditionalFiles {
			path := filepath.Join(modelsPath, file.Filename)
			Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
			Expect(os.WriteFile(path, []byte("test weights"), 0600)).To(Succeed())
			file.SHA256 = ""
			definition.Files = append(definition.Files, file)
		}
		_, err = gallery.InstallModel(context.Background(), state, entry.Name, &definition, entry.Overrides, nil, false)
		Expect(err).NotTo(HaveOccurred())
		data, err = os.ReadFile(filepath.Join(modelsPath, entry.Name+".yaml"))
		Expect(err).NotTo(HaveOccurred())
		var persisted map[string]any
		Expect(yaml.Unmarshal(data, &persisted)).To(Succeed())
		Expect(persisted).To(HaveKeyWithValue("context_size", 8192))
		Expect(persisted).To(HaveKeyWithValue("f16", true))
		Expect(persisted).To(HaveKeyWithValue("mmap", true))
		Expect(misplacedRootOptions(persisted, entry.Name)).To(BeEmpty())
		Expect(persisted["parameters"]).To(HaveKeyWithValue("model", "Qwen3-VL-4B-Thinking-Q4_K_M.gguf"))
		Expect(persisted).NotTo(HaveKey("model"))
		var loaded config.ModelConfig
		Expect(yaml.Unmarshal(data, &loaded)).To(Succeed())
		Expect(loaded.ContextSize).To(HaveValue(Equal(8192)))
		Expect(loaded.F16).To(HaveValue(BeTrue()))
		Expect(loaded.MMap).To(HaveValue(BeTrue()))
	})
})
