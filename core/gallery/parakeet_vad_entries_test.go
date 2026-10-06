package gallery_test

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// The parakeet-cpp VAD entries serve the VAD endpoint through known_usecases
// and the vad_model option points at a file the same entry downloads. A typo in
// either one installs cleanly and fails at the first request.
var _ = Describe("gallery/index.yaml parakeet-cpp VAD entries", func() {
	byName := func() map[string]gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		m := map[string]gallery.GalleryModel{}
		for _, e := range entries {
			m[e.Name] = e
		}
		return m
	}
	filenames := func(e gallery.GalleryModel) []string {
		var out []string
		for _, f := range e.AdditionalFiles {
			out = append(out, f.Filename)
		}
		return out
	}

	It("declares the VAD usecase and an existing model file on each VAD-only entry", func() {
		entries := byName()
		for _, name := range []string{
			"parakeet-cpp-silero-vad-f16",
			"parakeet-cpp-vad-moondream-redux-packed",
			"parakeet-cpp-vad-moondream-ultra-q8_0",
		} {
			e, ok := entries[name]
			Expect(ok).To(BeTrue(), name)
			Expect(e.Overrides["backend"]).To(Equal("parakeet-cpp"), name)
			Expect(e.Overrides["known_usecases"]).To(ConsistOf("vad"), name)
			params, _ := e.Overrides["parameters"].(map[string]any)
			Expect(filenames(e)).To(ContainElement(params["model"]), name)
			for _, f := range e.AdditionalFiles {
				Expect(f.SHA256).To(HaveLen(64), name)
			}
		}
	})

	It("shares the VAD head files with the matching ASR entries", func() {
		entries := byName()
		pairs := map[string]string{
			"parakeet-cpp-vad-moondream-redux-packed": "parakeet-cpp-moondream-redux-packed",
			"parakeet-cpp-vad-moondream-ultra-q8_0":   "parakeet-cpp-moondream-ultra-q8_0",
		}
		for vad, asr := range pairs {
			Expect(entries[vad].AdditionalFiles[0].Filename).To(Equal(entries[asr].AdditionalFiles[0].Filename))
			Expect(entries[vad].AdditionalFiles[0].SHA256).To(Equal(entries[asr].AdditionalFiles[0].SHA256))
		}
	})

	It("serves the VAD-only slices from files with the published checksums", func() {
		entries := byName()
		slices := map[string][2]string{
			"parakeet-cpp-vad-moondream-redux": {"parakeet-cpp/redux-vad.gguf", "588e1d6e2ee5b6cdfd9ec5ea98dc0993d5bea498d9cc4ec8d6077041eef8a34f"},
			"parakeet-cpp-vad-moondream-ultra": {"parakeet-cpp/ultra-vad-q8_0.gguf", "8b891a4435e97438104ca07c72530d0c5fe62b986baee48b2dd4e1500c1d4758"},
		}
		for name, want := range slices {
			e, ok := entries[name]
			Expect(ok).To(BeTrue(), name)
			Expect(e.Overrides["backend"]).To(Equal("parakeet-cpp"), name)
			Expect(e.Overrides["known_usecases"]).To(ConsistOf("vad"), name)
			Expect(e.License).To(Equal("cc-by-4.0"), name)
			Expect(e.Overrides["parameters"]).To(HaveKeyWithValue("model", want[0]), name)
			Expect(e.AdditionalFiles).To(HaveLen(1), name)
			Expect(e.AdditionalFiles[0].Filename).To(Equal(want[0]), name)
			Expect(e.AdditionalFiles[0].SHA256).To(Equal(want[1]), name)
			Expect(e.AdditionalFiles[0].URI).To(HavePrefix("https://huggingface.co/mudler/parakeet-cpp-gguf/resolve/main/"), name)
			Expect(e.Variants).To(BeEmpty(), name)
		}
	})

	It("installs Silero from the parakeet-cpp-vad entry and declares no variants", func() {
		// Variant ranking prefers the larger build that fits, and these are
		// different detectors, so the entry must not offer a choice.
		meta, ok := byName()["parakeet-cpp-vad"]
		Expect(ok).To(BeTrue())
		Expect(meta.Variants).To(BeEmpty())
		Expect(filenames(meta)).To(ConsistOf("parakeet-cpp/silero-vad-f16.gguf"))
		Expect(meta.Overrides["parameters"]).To(HaveKeyWithValue("model", "parakeet-cpp/silero-vad-f16.gguf"))
	})

	It("downloads the Silero file that the vad_model option of the v3 entry names", func() {
		e, ok := byName()["parakeet-cpp-tdt-0.6b-v3-silero-vad"]
		Expect(ok).To(BeTrue())
		opts, _ := e.Overrides["options"].([]any)
		var vadModel string
		for _, o := range opts {
			if s, ok := o.(string); ok && strings.HasPrefix(s, "vad_model:") {
				vadModel = strings.TrimPrefix(s, "vad_model:")
			}
		}
		Expect(vadModel).ToNot(BeEmpty())
		Expect(filenames(e)).To(ContainElement(filepath.ToSlash(vadModel)))
	})

	It("leaves the existing Silero entries on their own backends", func() {
		entries := byName()
		Expect(entries["silero-vad-ggml"].Overrides["backend"]).To(Equal("whisper"))
		Expect(entries["silero-vad"].Overrides["backend"]).To(Equal("silero-vad"))
	})
})
