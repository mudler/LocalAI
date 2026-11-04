package config_test

import (
	. "github.com/mudler/LocalAI/core/config"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An mmproj with clip.has_audio_encoder carries the audio input side of an
// any-to-any model (LFM3-Audio). It is not the gen-audio decoder, so the
// importer uses it to tell a speech-to-speech model from a TTS-only one.
var _ = Describe("HasAudioEncoderProjector", func() {
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

	It("detects a projector with an audio encoder", func() {
		f := mmproj("clip.has_audio_encoder", gguf.GGUFMetadataValueTypeBool, true)
		Expect(HasAudioEncoderProjector(f)).To(BeTrue())
	})

	It("is false for a vision projector", func() {
		f := mmproj("clip.has_vision_encoder", gguf.GGUFMetadataValueTypeBool, true)
		Expect(HasAudioEncoderProjector(f)).To(BeFalse())
	})

	It("is false when the flag is present but off", func() {
		f := mmproj("clip.has_audio_encoder", gguf.GGUFMetadataValueTypeBool, false)
		Expect(HasAudioEncoderProjector(f)).To(BeFalse())
	})

	It("is false, not a panic, when the flag carries the wrong type", func() {
		f := mmproj("clip.has_audio_encoder", gguf.GGUFMetadataValueTypeString, "true")
		Expect(HasAudioEncoderProjector(f)).To(BeFalse())
	})

	It("is false for a nil file", func() {
		Expect(HasAudioEncoderProjector(nil)).To(BeFalse())
	})
})

// Qwen3-TTS sets both encoder flags, so the speech-to-speech check must read
// the encoder family as well as the flags.
var _ = Describe("HasSpeechToSpeechProjector", func() {
	projector := func(kvs ...gguf.GGUFMetadataKV) *gguf.GGUFFile {
		kvs = append([]gguf.GGUFMetadataKV{{Key: "general.architecture", ValueType: gguf.GGUFMetadataValueTypeString, Value: "clip"}}, kvs...)
		return &gguf.GGUFFile{Header: gguf.GGUFHeader{MetadataKV: kvs}}
	}
	flag := func(key string) gguf.GGUFMetadataKV {
		return gguf.GGUFMetadataKV{Key: key, ValueType: gguf.GGUFMetadataValueTypeBool, Value: true}
	}
	family := func(value string) gguf.GGUFMetadataKV {
		return gguf.GGUFMetadataKV{Key: "clip.audio.projector_type", ValueType: gguf.GGUFMetadataValueTypeString, Value: value}
	}

	It("detects an LFM3 audio encoder with the gen-audio decoder", func() {
		f := projector(flag("clip.has_audio_encoder"), flag("clip.has_gen_audio_encoder"), family("lfm3a"))
		Expect(HasSpeechToSpeechProjector(f)).To(BeTrue())
	})

	It("is false for the Qwen3-TTS speaker encoder with the gen-audio decoder", func() {
		f := projector(flag("clip.has_audio_encoder"), flag("clip.has_gen_audio_encoder"), family("qwen3tts_spkenc"))
		Expect(HasSpeechToSpeechProjector(f)).To(BeFalse())
	})

	It("is false for an LFM3 encoder without the gen-audio decoder", func() {
		f := projector(flag("clip.has_audio_encoder"), family("lfm3a"))
		Expect(HasSpeechToSpeechProjector(f)).To(BeFalse())
	})

	It("is false for the gen-audio decoder without an audio encoder", func() {
		f := projector(flag("clip.has_gen_audio_encoder"), family("lfm3a"))
		Expect(HasSpeechToSpeechProjector(f)).To(BeFalse())
	})

	It("is false, not a panic, when the encoder family has the wrong type", func() {
		f := projector(flag("clip.has_audio_encoder"), flag("clip.has_gen_audio_encoder"),
			gguf.GGUFMetadataKV{Key: "clip.audio.projector_type", ValueType: gguf.GGUFMetadataValueTypeBool, Value: true})
		Expect(HasSpeechToSpeechProjector(f)).To(BeFalse())
	})

	It("is false for a nil file", func() {
		Expect(HasSpeechToSpeechProjector(nil)).To(BeFalse())
	})
})
