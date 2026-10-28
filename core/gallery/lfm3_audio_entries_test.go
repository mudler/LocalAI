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

	type shape struct {
		model    string // weight file the entry points the backend at
		pipeline bool   // chained pipeline instead of one speech-to-speech session
		vad      bool
	}
	shapes := map[string]shape{
		"lfm3-audio-2b":                 {model: "LFM3-Audio-2B-Q4_K_M.gguf"},
		"lfm3-audio-2b-q8":              {model: "LFM3-Audio-2B-Q8_0.gguf"},
		"lfm3-audio-2b-vad":             {model: "LFM3-Audio-2B-Q4_K_M.gguf", vad: true},
		"lfm3-audio-2b-q8-vad":          {model: "LFM3-Audio-2B-Q8_0.gguf", vad: true},
		"lfm3-audio-2b-pipeline":        {model: "LFM3-Audio-2B-Q4_K_M.gguf", pipeline: true},
		"lfm3-audio-2b-q8-pipeline":     {model: "LFM3-Audio-2B-Q8_0.gguf", pipeline: true},
		"lfm3-audio-2b-pipeline-vad":    {model: "LFM3-Audio-2B-Q4_K_M.gguf", pipeline: true, vad: true},
		"lfm3-audio-2b-q8-pipeline-vad": {model: "LFM3-Audio-2B-Q8_0.gguf", pipeline: true, vad: true},
	}

	It("declares every any-to-any and chained-pipeline combination of quantization and VAD", func() {
		entries := byName()
		for name, want := range shapes {
			e, ok := entries[name]
			Expect(ok).To(BeTrue(), name)
			Expect(e.Overrides["backend"]).To(Equal("llama-cpp"), name)
			Expect(e.License).To(Equal("lfm1.0"), name)
			Expect(e.Overrides["options"]).To(ConsistOf("parallel:2"), name)
			Expect(e.Overrides["mmproj"]).To(Equal(mmprojQ8), name)
			Expect(e.Overrides["template"]).To(HaveKeyWithValue("use_tokenizer_template", true), name)
			Expect(e.Overrides["parameters"]).To(HaveKeyWithValue("model", want.model), name)
			Expect(e.Overrides["tts"]).To(HaveKeyWithValue("voice", "female"), name)

			pipe, _ := e.Overrides["pipeline"].(map[string]any)
			if want.vad {
				Expect(pipe).To(HaveKeyWithValue("vad", "silero-vad-ggml"), name)
				Expect(strings.Join(strings.Fields(e.Description), " ")).To(ContainSubstring("install the silero-vad-ggml model first"), name)
			} else {
				Expect(pipe).ToNot(HaveKey("vad"), name)
			}
			if want.pipeline {
				// A chained pipeline is not a realtime_audio model: llama-cpp
				// models of that kind must be any-to-any, so the entry names
				// itself in each slot instead.
				Expect(e.Overrides["known_usecases"]).To(ConsistOf("chat", "transcript", "tts"), name)
				Expect(pipe).ToNot(HaveKey("any_to_any"), name)
				for _, slot := range []string{"llm", "transcription", "tts"} {
					Expect(pipe).To(HaveKeyWithValue(slot, name), name+" "+slot)
				}
			} else {
				Expect(e.Overrides["known_usecases"]).To(ConsistOf("realtime_audio", "chat", "transcript", "tts"), name)
				Expect(pipe).To(HaveKeyWithValue("any_to_any", true), name)
				Expect(pipe).ToNot(HaveKey("llm"), name)
			}
		}
	})

	It("points every VAD entry at a VAD model that exists in the gallery", func() {
		vad, ok := byName()["silero-vad-ggml"]
		Expect(ok).To(BeTrue())
		Expect(vad.Overrides["known_usecases"]).To(ConsistOf("vad"))
	})

	// The gallery has no model-to-model dependencies, so the entries stay
	// independent. A variants list on the Q4 entry would let the installer
	// pick the larger Q8 build on any host with enough memory, which is not
	// what a plain install of lfm3-audio-2b should do.
	It("keeps every entry independent so a plain install gets exactly the named build", func() {
		entries := byName()
		for name := range shapes {
			Expect(entries[name].Variants).To(BeEmpty(), name)
		}
		for _, e := range entries {
			for _, v := range e.Variants {
				Expect(shapes).ToNot(HaveKey(v.Model), e.Name+" offers "+v.Model+" as a variant")
			}
		}
		// Nothing to select between, whatever memory the host has.
		base := entries["lfm3-audio-2b"]
		desc, err := gallery.DescribeVariants(nil, &base, gallery.ResolveEnv{AvailableMemory: 1 << 40})
		Expect(err).ToNot(HaveOccurred())
		Expect(desc).To(BeNil())
	})

	It("installs each weight and projector with the published sha256 from the GGUF repo", func() {
		entries := byName()
		shaOf := map[string]string{
			"LFM3-Audio-2B-Q4_K_M.gguf": "9a773289bd321ed879cd3a76545f6aca506e403851a45fb315420aee45625be2",
			"LFM3-Audio-2B-Q8_0.gguf":   "d4e36902ba8f0ee7b333596030a59d650f88ac76660b4d9ba5ff77c3c2258fc0",
		}
		want := map[string]map[string]string{}
		for name, sh := range shapes {
			want[name] = map[string]string{sh.model: shaOf[sh.model], mmprojQ8: mmprojSHA}
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
