// SPDX-License-Identifier: MIT
package gallery_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// The LFM3-Audio entries are any-to-any models: the llama-cpp backend serves
// the realtime speech-to-speech session, transcription and TTS from one loaded
// model, so the pipeline flag, the parallel slot and the mmproj must all agree
// with what the importer writes for the same projector. A typo here installs
// cleanly and fails at the first session.
var _ = Describe("gallery/index.yaml LFM3-Audio entries", func() {
	const (
		repoResolve = "https://huggingface.co/LiquidAI/LFM3-Audio-2B-GGUF/resolve/fcfae9027c00a6f3f872c48a841cfeb77e2b5a1c/"
		mmprojQ8    = "mmproj-LFM3-Audio-2B-Q8_0.gguf"
		mmprojSHA   = "26601e904245a85bd8f3cf320a629e897cfc01c1d09369e8b4fd951f13024d3e"
	)

	byName := func() map[string]gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		return indexEntriesByName(entries)
	}
	fileByName := func(e gallery.GalleryModel, name string) (gallery.File, bool) {
		for _, f := range e.AdditionalFiles {
			if f.Filename == name {
				return f, true
			}
		}
		return gallery.File{}, false
	}

	It("declares the default Q4_K_M entry as a llama-cpp any-to-any model", func() {
		e, ok := byName()["lfm3-audio-2b"]
		Expect(ok).To(BeTrue())
		Expect(e.Overrides["backend"]).To(Equal("llama-cpp"))
		Expect(e.License).To(Equal("lfm1.0"))
		Expect(e.Overrides["known_usecases"]).To(ConsistOf("realtime_audio", "chat", "transcript", "tts"))
		Expect(e.Overrides["pipeline"]).To(HaveKeyWithValue("any_to_any", true))
		Expect(e.Overrides["pipeline"]).ToNot(HaveKey("vad"), "VAD stays optional, so the entry does not name a VAD model")
		Expect(e.Overrides["options"]).To(ConsistOf("parallel:2"))
		Expect(e.Overrides["mmproj"]).To(Equal(mmprojQ8))
		Expect(e.Overrides["template"]).To(HaveKeyWithValue("use_tokenizer_template", true))
		Expect(e.Overrides["parameters"]).To(HaveKeyWithValue("model", "LFM3-Audio-2B-Q4_K_M.gguf"))
		Expect(e.Variants).To(HaveLen(1))
		Expect(e.Variants[0].Model).To(Equal("lfm3-audio-2b-q8"))
	})

	It("declares the Q8_0 entry with the same projector and no variants of its own", func() {
		e, ok := byName()["lfm3-audio-2b-q8"]
		Expect(ok).To(BeTrue())
		Expect(e.Overrides["backend"]).To(Equal("llama-cpp"))
		Expect(e.License).To(Equal("lfm1.0"))
		Expect(e.Overrides["known_usecases"]).To(ConsistOf("realtime_audio", "chat", "transcript", "tts"))
		Expect(e.Overrides["pipeline"]).To(HaveKeyWithValue("any_to_any", true))
		Expect(e.Overrides["options"]).To(ConsistOf("parallel:2"))
		Expect(e.Overrides["mmproj"]).To(Equal(mmprojQ8))
		Expect(e.Overrides["parameters"]).To(HaveKeyWithValue("model", "LFM3-Audio-2B-Q8_0.gguf"))
		Expect(e.Variants).To(BeEmpty())
	})

	It("installs each weight and projector with the published sha256 from the GGUF repo", func() {
		entries := byName()
		want := map[string]map[string]string{
			"lfm3-audio-2b": {
				"LFM3-Audio-2B-Q4_K_M.gguf": "9a773289bd321ed879cd3a76545f6aca506e403851a45fb315420aee45625be2",
				mmprojQ8:                    mmprojSHA,
			},
			"lfm3-audio-2b-q8": {
				"LFM3-Audio-2B-Q8_0.gguf": "d4e36902ba8f0ee7b333596030a59d650f88ac76660b4d9ba5ff77c3c2258fc0",
				mmprojQ8:                  mmprojSHA,
			},
		}
		for name, files := range want {
			e := entries[name]
			Expect(e.AdditionalFiles).To(HaveLen(len(files)), name)
			for filename, sha := range files {
				f, ok := fileByName(e, filename)
				Expect(ok).To(BeTrue(), name+": "+filename)
				Expect(f.SHA256).To(Equal(sha), name+": "+filename)
				Expect(f.URI).To(Equal(repoResolve+filename), name+": "+filename)
				Expect(strings.HasPrefix(f.URI, "https://huggingface.co/LiquidAI/LFM3-Audio-2B-GGUF/")).To(BeTrue(), name)
			}
		}
	})
})
