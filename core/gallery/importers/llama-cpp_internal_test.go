package importers

import (
	"github.com/mudler/LocalAI/core/gallery"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The gen-audio probe has to find the mmproj the import actually selected.
// modelConfig.MMProj holds the TARGET filename the file will be written to,
// which is not the URI it is fetched from, so the two have to be joined
// through cfg.Files or the probe silently never runs and every Qwen3-TTS
// import stays labelled as chat.
var _ = Describe("pickMMProjProbeURL", func() {
	cfgWith := func(files ...gallery.File) *gallery.ModelConfig {
		return &gallery.ModelConfig{Files: files}
	}

	It("resolves the URI of the selected mmproj", func() {
		cfg := cfgWith(
			gallery.File{Filename: "llama-cpp/models/tts/model.gguf", URI: "https://example.invalid/model.gguf"},
			gallery.File{Filename: "llama-cpp/mmproj/tts/mmproj.gguf", URI: "https://example.invalid/mmproj.gguf"},
		)
		Expect(pickMMProjProbeURL("llama-cpp/mmproj/tts/mmproj.gguf", cfg)).To(Equal("https://example.invalid/mmproj.gguf"))
	})

	It("resolves a huggingface:// mmproj URI to a fetchable URL", func() {
		cfg := cfgWith(gallery.File{
			Filename: "mmproj.gguf",
			URI:      "huggingface://ggml-org/Qwen3-TTS-12Hz-1.7B-Base-GGUF/mmproj-Qwen3-TTS-12Hz-1.7B-Base-Q8_0.gguf",
		})
		Expect(pickMMProjProbeURL("mmproj.gguf", cfg)).To(HavePrefix("https://"))
	})

	It("returns nothing when the import selected no mmproj", func() {
		cfg := cfgWith(gallery.File{Filename: "model.gguf", URI: "https://example.invalid/model.gguf"})
		Expect(pickMMProjProbeURL("", cfg)).To(BeEmpty())
	})

	It("returns nothing when the mmproj is not among the files", func() {
		cfg := cfgWith(gallery.File{Filename: "model.gguf", URI: "https://example.invalid/model.gguf"})
		Expect(pickMMProjProbeURL("mmproj.gguf", cfg)).To(BeEmpty())
	})

	// OCI/Ollama artifacts are not range-fetchable as a GGUF byte stream, the
	// same reason the MTP probe skips them.
	It("returns nothing for an OCI artifact", func() {
		cfg := cfgWith(gallery.File{Filename: "mmproj.gguf", URI: "oci://quay.io/example/model:latest"})
		Expect(pickMMProjProbeURL("mmproj.gguf", cfg)).To(BeEmpty())
	})

	It("returns nothing for a nil config", func() {
		Expect(pickMMProjProbeURL("mmproj.gguf", nil)).To(BeEmpty())
	})
})
