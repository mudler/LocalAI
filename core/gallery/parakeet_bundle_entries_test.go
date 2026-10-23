package gallery_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// The parakeet-cpp bundle entries install one GGUF file that holds several
// models. The component options name components inside that file, so a typo
// installs cleanly and fails when the model loads. The sizes and checksums are
// those of the files published in mudler/parakeet-cpp-gguf.
var _ = Describe("gallery/index.yaml parakeet-cpp bundle entries", func() {
	type bundle struct {
		file     string
		sha256   string
		usecases []string
		options  []string
	}
	full := []string{"vad:true", "diar_component:diar", "sound_component:ced", "speaker_component:voice"}
	want := map[string]bundle{
		"parakeet-cpp-bundle-small": {
			"parakeet-cpp/parakeet-bundle-small.gguf",
			"5f2c6697eb08d4d67e858692d4223a9d911aacc51ae16cf2aee9e760f8a2269a",
			[]string{"transcript", "vad", "diarization", "sound_classification", "speaker_recognition"}, full,
		},
		"parakeet-cpp-bundle-standard": {
			"parakeet-cpp/parakeet-bundle-standard.gguf",
			"85304e3bb49de04b84a7d0c76baaa7dedd1a0c02c0920078a99f92c00cffbdf6",
			[]string{"transcript", "vad", "diarization", "sound_classification", "speaker_recognition"}, full,
		},
		"parakeet-cpp-bundle-moondream-redux": {
			"parakeet-cpp/parakeet-bundle-moondream-redux.gguf",
			"d7c5bff66b1dbc02f4c7a83148b0836b57805676c844193c214c751249853d7b",
			[]string{"transcript", "vad"}, []string{"vad:true"},
		},
	}

	entries := func() map[string]gallery.GalleryModel {
		list, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		m := map[string]gallery.GalleryModel{}
		for _, e := range list {
			m[e.Name] = e
		}
		return m
	}

	It("installs one bundle file with the published checksum and serves it as the model", func() {
		all := entries()
		for name, w := range want {
			e, ok := all[name]
			Expect(ok).To(BeTrue(), name)
			Expect(e.Overrides["backend"]).To(Equal("parakeet-cpp"), name)
			Expect(e.Overrides["name"]).To(Equal(name), name)
			Expect(e.Overrides["parameters"]).To(HaveKeyWithValue("model", w.file), name)
			Expect(e.AdditionalFiles).To(HaveLen(1), name)
			Expect(e.AdditionalFiles[0].Filename).To(Equal(w.file), name)
			Expect(e.AdditionalFiles[0].SHA256).To(Equal(w.sha256), name)
			Expect(e.AdditionalFiles[0].URI).To(Equal("huggingface://mudler/parakeet-cpp-gguf/"+strings.TrimPrefix(w.file, "parakeet-cpp/")), name)
			Expect(e.Variants).To(BeEmpty(), name)
		}
	})

	// speaker_recognition means the model answers the /v1/voice/* RPCs
	// (VoiceEmbed, VoiceVerify). The parakeet-cpp backend does since it binds
	// parakeet_capi_speaker_embed_pcm, so entries with a speaker encoder declare it.
	It("declares the usecases and options of the roles each bundle covers", func() {
		all := entries()
		for name, w := range want {
			e := all[name]
			Expect(e.Overrides["known_usecases"]).To(ConsistOf(toAny(w.usecases)...), name)
			Expect(e.Overrides["options"]).To(ConsistOf(toAny(w.options)...), name)
		}
	})

	It("declares speaker recognition only where the model has a speaker encoder", func() {
		all := entries()
		for _, name := range []string{"parakeet-cpp-bundle-small", "parakeet-cpp-bundle-standard", "parakeet-cpp-realtime-scene-speakers"} {
			Expect(all[name].Overrides["known_usecases"]).To(ContainElement("speaker_recognition"), name)
		}
		for _, name := range []string{"parakeet-cpp-bundle-moondream-redux", "parakeet-cpp-tdt_ctc-110m"} {
			Expect(all[name].Overrides["known_usecases"]).ToNot(ContainElement("speaker_recognition"), name)
		}
	})

	It("states that the licence is per component and names each one", func() {
		all := entries()
		for name := range want {
			e := all[name]
			Expect(e.License).To(Equal("other"), name)
			Expect(e.Description).To(ContainSubstring("no single license"), name)
			Expect(e.Description).To(ContainSubstring("CC-BY-4.0"), name)
			Expect(e.Description).To(ContainSubstring("MIT"), name)
		}
		for _, name := range []string{"parakeet-cpp-bundle-small", "parakeet-cpp-bundle-standard"} {
			d := all[name].Description
			Expect(d).To(ContainSubstring("OpenMDW-1.1"), name)
			Expect(d).To(ContainSubstring("Apache-2.0"), name)
			Expect(d).To(ContainSubstring("not consistent upstream"), name)
			Expect(d).To(ContainSubstring("converted, not trained"), name)
		}
		Expect(all["parakeet-cpp-bundle-moondream-redux"].Description).To(ContainSubstring("CPU only"))
	})

	It("keeps the single-purpose parakeet-cpp entries", func() {
		all := entries()
		for _, name := range []string{
			"parakeet-cpp-tdt_ctc-110m", "parakeet-cpp-tdt-0.6b-v3", "parakeet-cpp-moondream-redux-packed",
			"parakeet-cpp-silero-vad-f16", "parakeet-cpp-nemotron-3-diarization", "parakeet-cpp-ced-tiny",
		} {
			Expect(all).To(HaveKey(name))
		}
	})
})

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
