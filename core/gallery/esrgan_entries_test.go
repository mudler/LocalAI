// SPDX-License-Identifier: MIT
package gallery_test

import (
	"github.com/mudler/LocalAI/core/config"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("gallery/index.yaml ESRGAN entry", func() {
	It("installs the official native x4 checkpoint as an upscale-only model", func() {
		entries, err := loadGalleryIndex()
		Expect(err).NotTo(HaveOccurred())
		entry, ok := indexEntriesByName(entries)["realesrgan-x4plus-anime-6b"]
		Expect(ok).To(BeTrue())
		Expect(entry.URL).To(Equal("github:mudler/LocalAI/gallery/virtual.yaml@master"))
		Expect(entry.License).To(Equal("BSD-3-Clause"))
		Expect(entry.Overrides["backend"]).To(Equal("stablediffusion-ggml"))
		Expect(entry.Overrides["known_usecases"]).To(ConsistOf("upscale"))
		Expect(entry.Overrides["upscale_scale"]).To(Equal(4))
		Expect(entry.Overrides["upscale_tile_size"]).To(Equal(128))
		Expect(entry.Overrides).NotTo(HaveKey("options"))
		Expect(entry.AdditionalFiles).To(HaveLen(1))
		file := entry.AdditionalFiles[0]
		Expect(file.Filename).To(Equal("RealESRGAN_x4plus_anime_6B.pth"))
		Expect(entry.Overrides["parameters"]).To(HaveKeyWithValue("model", file.Filename))
		Expect(file.URI).To(Equal("https://github.com/xinntao/Real-ESRGAN/releases/download/v0.2.2.4/" + file.Filename))
		Expect(file.SHA256).To(Equal("f872d837d3c90ed2e05227bed711af5671a6fd1c9f7d7e91c911a61f155e99da"))

		// Keep the copy-paste configuration in the documentation aligned with the
		// installable entry, including YAML scalar types.
		doc, err := os.ReadFile("../../docs/content/features/image-generation.md")
		Expect(err).NotTo(HaveOccurred())
		const start = "```yaml\nname: realesrgan-x4plus-anime-6b\n"
		_, example, found := strings.Cut(string(doc), start)
		Expect(found).To(BeTrue())
		example, _, found = strings.Cut(example, "```")
		Expect(found).To(BeTrue())
		var settings map[string]any
		Expect(yaml.Unmarshal([]byte(example), &settings)).To(Succeed())
		Expect(settings).To(Equal(entry.Overrides))
	})
})

var _ = Describe("Diffusers upscaler gallery capability", func() {
	It("explicitly declares upscaling rather than image generation", func() {
		entries, err := loadGalleryIndex()
		Expect(err).NotTo(HaveOccurred())
		entry, ok := indexEntriesByName(entries)["stable-diffusion-x4-upscaler"]
		Expect(ok).To(BeTrue())
		data, err := yaml.Marshal(entry.Overrides)
		Expect(err).NotTo(HaveOccurred())
		var cfg config.ModelConfig
		Expect(yaml.Unmarshal(data, &cfg)).To(Succeed())
		Expect(cfg.HasUsecases(config.FLAG_UPSCALE)).To(BeTrue())
		Expect(cfg.HasUsecases(config.FLAG_IMAGE)).To(BeFalse())
		Expect(config.GetBackendCapability(cfg.Backend).GRPCMethods).To(ContainElement(config.MethodUpscaleImage))
	})
})
