package config_test

import (
	. "github.com/mudler/LocalAI/core/config"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ggufWithArch fabricates a minimal in-memory GGUF carrying the given
// `general.architecture` and a positive `<arch>.nextn_predict_layers` count,
// so HasEmbeddedMTPHead can be exercised without a real model file.
func ggufWithArch(arch string, nextn uint32) *gguf.GGUFFile {
	return &gguf.GGUFFile{
		Header: gguf.GGUFHeader{
			MetadataKV: gguf.GGUFMetadataKVs{
				{
					Key:       "general.architecture",
					ValueType: gguf.GGUFMetadataValueTypeString,
					Value:     arch,
				},
				{
					Key:       arch + ".nextn_predict_layers",
					ValueType: gguf.GGUFMetadataValueTypeUint32,
					Value:     nextn,
				},
			},
		},
	}
}

var _ = Describe("MTP auto-defaults", func() {
	Context("MTPSpecOptions", func() {
		It("returns the upstream-recommended speculative tuple", func() {
			Expect(MTPSpecOptions()).To(Equal([]string{
				"spec_type:draft-mtp",
				"spec_n_max:6",
				"spec_p_min:0.75",
			}))
		})

		It("returns a defensive copy so callers cannot mutate the package default", func() {
			opts := MTPSpecOptions()
			opts[0] = "spec_type:none"
			Expect(MTPSpecOptions()[0]).To(Equal("spec_type:draft-mtp"))
		})
	})

	Context("ApplyMTPDefaults", func() {
		It("appends MTP options when nothing is configured", func() {
			cfg := &ModelConfig{Name: "qwen-mtp"}
			ApplyMTPDefaults(cfg, 1)
			Expect(cfg.Options).To(Equal([]string{
				"spec_type:draft-mtp",
				"spec_n_max:6",
				"spec_p_min:0.75",
			}))
		})

		It("preserves unrelated options already on the config", func() {
			cfg := &ModelConfig{
				Name:    "qwen-mtp",
				Options: []string{"use_jinja:true", "cache_reuse:256"},
			}
			ApplyMTPDefaults(cfg, 1)
			Expect(cfg.Options).To(Equal([]string{
				"use_jinja:true",
				"cache_reuse:256",
				"spec_type:draft-mtp",
				"spec_n_max:6",
				"spec_p_min:0.75",
			}))
		})

		It("is a no-op when the user already configured spec_type", func() {
			cfg := &ModelConfig{
				Name:    "qwen-mtp",
				Options: []string{"spec_type:ngram-simple", "use_jinja:true"},
			}
			ApplyMTPDefaults(cfg, 1)
			Expect(cfg.Options).To(Equal([]string{
				"spec_type:ngram-simple",
				"use_jinja:true",
			}))
		})

		It("also respects the legacy speculative_type alias", func() {
			cfg := &ModelConfig{
				Name:    "qwen-mtp",
				Options: []string{"speculative_type:ngram-mod"},
			}
			ApplyMTPDefaults(cfg, 1)
			Expect(cfg.Options).To(Equal([]string{"speculative_type:ngram-mod"}))
		})

		It("tolerates a nil config", func() {
			Expect(func() { ApplyMTPDefaults(nil, 1) }).ToNot(Panic())
		})
	})

	Context("HasEmbeddedMTPHead", func() {
		It("returns false on a nil GGUF file", func() {
			n, ok := HasEmbeddedMTPHead(nil)
			Expect(ok).To(BeFalse())
			Expect(n).To(BeZero())
		})

		It("detects a same-GGUF embedded head (DeepSeek/Qwen style)", func() {
			n, ok := HasEmbeddedMTPHead(ggufWithArch("qwen3moe", 1))
			Expect(ok).To(BeTrue())
			Expect(n).To(Equal(uint32(1)))
		})

		It("ignores a gemma4-assistant draft-only model", func() {
			// The assistant GGUF carries nextn_predict_layers but is a separate
			// draft model that requires a paired target (ctx_other); it cannot
			// self-speculate, so it must not trigger the embedded-head defaults.
			n, ok := HasEmbeddedMTPHead(ggufWithArch("gemma4-assistant", 48))
			Expect(ok).To(BeFalse())
			Expect(n).To(BeZero())
		})
	})
})

// The mmproj of a Qwen3-TTS GGUF repo is named exactly like a vision
// projector, so the gen-audio flag llama.cpp's own mtmd gates its speech
// pipeline on is the only thing that tells the two apart.
var _ = Describe("HasGenAudioProjector", func() {
	mmproj := func(key string, valueType gguf.GGUFMetadataValueType, value any) *gguf.GGUFFile {
		return &gguf.GGUFFile{
			Header: gguf.GGUFHeader{
				MetadataKV: gguf.GGUFMetadataKVs{
					{Key: "general.architecture", ValueType: gguf.GGUFMetadataValueTypeString, Value: "clip"},
					{Key: key, ValueType: valueType, Value: value},
				},
			},
		}
	}

	It("detects the gen-audio projector Qwen3-TTS ships", func() {
		f := mmproj("clip.has_gen_audio_encoder", gguf.GGUFMetadataValueTypeBool, true)
		Expect(HasGenAudioProjector(f)).To(BeTrue())
	})

	It("is false for a vision projector", func() {
		f := mmproj("clip.has_vision_encoder", gguf.GGUFMetadataValueTypeBool, true)
		Expect(HasGenAudioProjector(f)).To(BeFalse())
	})

	// A speaker encoder alone is reference-audio INPUT. The gen-audio decoder
	// is what makes the model emit speech, and Qwen3-TTS carries both.
	It("is false for a projector that only encodes speaker audio", func() {
		f := mmproj("clip.has_audio_encoder", gguf.GGUFMetadataValueTypeBool, true)
		Expect(HasGenAudioProjector(f)).To(BeFalse())
	})

	It("is false when the flag is present but off", func() {
		f := mmproj("clip.has_gen_audio_encoder", gguf.GGUFMetadataValueTypeBool, false)
		Expect(HasGenAudioProjector(f)).To(BeFalse())
	})

	// ValueBool panics on a type mismatch, and this runs against arbitrary
	// user-supplied repos.
	It("is false, not a panic, when the flag carries the wrong type", func() {
		f := mmproj("clip.has_gen_audio_encoder", gguf.GGUFMetadataValueTypeString, "true")
		Expect(HasGenAudioProjector(f)).To(BeFalse())
	})

	It("is false for a nil file", func() {
		Expect(HasGenAudioProjector(nil)).To(BeFalse())
	})
})
