// SPDX-License-Identifier: MIT

package gallery_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("gallery/index.yaml Yue2 entry", func() {
	It("installs the complete Q8_0 and F16 package as an audio-cpp model directory", func() {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())

		models := make([]*gallery.GalleryModel, 0, len(entries))
		for i := range entries {
			models = append(models, &entries[i])
		}
		entry := gallery.FindGalleryElement(models, "audio-cpp-yue2-3b")
		Expect(entry).ToNot(BeNil())
		Expect(entry.License).To(Equal("cc-by-nc-4.0"))
		Expect(entry.Tags).To(ContainElements("audio-cpp", "yue2", "music-generation", "text-to-audio", "sound-generation", "gguf", "quantized"))
		Expect(entry.Description).To(ContainSubstring("Q8_0"))
		Expect(entry.Description).To(ContainSubstring("F16 VAE"))

		Expect(entry.Overrides).To(HaveKeyWithValue("backend", "audio-cpp"))
		Expect(entry.Overrides).To(HaveKeyWithValue("name", "audio-cpp-yue2-3b"))
		Expect(entry.Overrides["known_usecases"]).To(ConsistOf("sound_generation"))
		Expect(entry.Overrides["parameters"]).To(HaveKeyWithValue("model", "audio-cpp/yue2-3b"))
		Expect(entry.Overrides["options"]).To(ConsistOf(
			"family:yue2",
			"task:gen",
			"backend:best",
			"session.yue2.model_gguf:yue2-3b-q8_0.gguf",
			"session.yue2.vae_gguf:yue2-vae-f16.gguf",
		))

		wantFiles := []gallery.File{
			{Filename: "audio-cpp/yue2-3b/yue2-3b-q8_0.gguf", URI: "huggingface://audio-cpp/Yue2-3B-GGUF/yue2-3b-q8_0.gguf", SHA256: "52be345b5155bf9ef0b2411e7df8d1d2b8e13b6d977a2f70c35612ec69b1f591"},
			{Filename: "audio-cpp/yue2-3b/yue2-vae-f16.gguf", URI: "huggingface://audio-cpp/Yue2-3B-GGUF/yue2-vae-f16.gguf", SHA256: "d4f4a05d8f291ae820cd1e43609da3fa91b56465810091a2b08c3350b751719d"},
			{Filename: "audio-cpp/yue2-3b/sidecars/yue2-model-config.json", URI: "huggingface://audio-cpp/Yue2-3B-GGUF/sidecars/yue2-model-config.json", SHA256: "ad3477bbef890bf98ae196c1e4b44779494a6231c4ab66f32708eabadf265329"},
			{Filename: "audio-cpp/yue2-3b/sidecars/yue2-generation-config.json", URI: "huggingface://audio-cpp/Yue2-3B-GGUF/sidecars/yue2-generation-config.json", SHA256: "203830cebde6e3644eb291925362990d198e66c3b6006cd11f1aaa21904bcc61"},
			{Filename: "audio-cpp/yue2-3b/sidecars/yue2-qwen.tiktoken", URI: "huggingface://audio-cpp/Yue2-3B-GGUF/sidecars/yue2-qwen.tiktoken", SHA256: "b2b1b8dfb5cc5f024bafc373121c6aba3f66f9a5a0269e243470a1de16a33186"},
			{Filename: "audio-cpp/yue2-3b/sidecars/yue2-vae-config.json", URI: "huggingface://audio-cpp/Yue2-3B-GGUF/sidecars/yue2-vae-config.json", SHA256: "f0191bb9694009956de44e0c361a6f1334760be4c8f848e599bde242a54a0970"},
		}
		Expect(entry.AdditionalFiles).To(ConsistOf(wantFiles))

		sidecars := 0
		for _, file := range entry.AdditionalFiles {
			if strings.Contains(file.Filename, "/sidecars/") {
				sidecars++
			}
		}
		Expect(sidecars).To(Equal(4))
	})
})
