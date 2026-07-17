package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMossTtsCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "moss-tts-cpp suite")
}

var _ = Describe("resolveVoice", func() {
	It("treats an audio path as a clone reference (case-insensitive ext)", func() {
		Expect(resolveVoice("/x/ref.WAV")).To(Equal("/x/ref.WAV"))
	})
	It("recognizes mp3/flac/ogg/m4a", func() {
		for _, p := range []string{"a.mp3", "b.flac", "c.ogg", "d.m4a"} {
			Expect(resolveVoice(p)).To(Equal(p))
		}
	})
	It("ignores a bare token (no named speakers in MOSS-TTS-Local)", func() {
		Expect(resolveVoice("serena")).To(BeEmpty())
	})
	It("returns empty for empty input", func() {
		Expect(resolveVoice("  ")).To(BeEmpty())
	})
})

var _ = Describe("parseOptions", func() {
	It("extracts codec, tokenizer, seed", func() {
		o := parseOptions([]string{
			"codec:codec.gguf", "tokenizer:tok.gguf", "seed:7", "unknown:ignored",
		})
		Expect(o.codecPath).To(Equal("codec.gguf"))
		Expect(o.tokenizerPath).To(Equal("tok.gguf"))
		Expect(o.seed).To(Equal(7))
	})
	It("accepts audio_tokenizer / text_tokenizer aliases", func() {
		o := parseOptions([]string{"audio_tokenizer:c.gguf", "text_tokenizer:t.gguf"})
		Expect(o.codecPath).To(Equal("c.gguf"))
		Expect(o.tokenizerPath).To(Equal("t.gguf"))
	})
	It("defaults seed -1", func() {
		Expect(parseOptions(nil).seed).To(Equal(-1))
	})
})

var _ = Describe("isAudioCodecName", func() {
	DescribeTable("distinguishes the audio codec from the text tokenizer",
		func(name string, want bool) {
			Expect(isAudioCodecName(name)).To(Equal(want))
		},
		Entry("audio tokenizer is the codec", "moss-audio-tokenizer-v2-f32.gguf", true),
		Entry("codec keyword", "some-codec.gguf", true),
		Entry("text tokenizer is not the codec", "moss-tokenizer-v1_5.gguf", false),
		Entry("model is not the codec", "moss-tts-local-v1_5-q8_0.gguf", false),
	)
})

var _ = Describe("discoverCodec / discoverTokenizer", func() {
	var dir, model, codec, tok string
	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		model = filepath.Join(dir, "moss-tts-local-v1_5-q8_0.gguf")
		codec = filepath.Join(dir, "moss-audio-tokenizer-v2-f32.gguf")
		tok = filepath.Join(dir, "moss-tokenizer-v1_5.gguf")
		for _, p := range []string{model, codec, tok} {
			Expect(os.WriteFile(p, []byte("x"), 0o644)).To(Succeed())
		}
	})
	It("finds the audio codec, excluding the model", func() {
		Expect(discoverCodec(dir, model)).To(Equal(codec))
	})
	It("finds the text tokenizer, excluding model and codec", func() {
		Expect(discoverTokenizer(dir, model, codec)).To(Equal(tok))
	})
})

var _ = Describe("wavHeaderStereo", func() {
	It("emits a 44-byte streaming WAV header at 48 kHz stereo 16-bit", func() {
		h := wavHeaderStereo(48000)
		Expect(h).To(HaveLen(44))
		Expect(string(h[0:4])).To(Equal("RIFF"))
		Expect(string(h[8:12])).To(Equal("WAVE"))
		Expect(string(h[12:16])).To(Equal("fmt "))
		Expect(string(h[36:40])).To(Equal("data"))
		var channels uint16
		Expect(binary.Read(bytes.NewReader(h[22:24]), binary.LittleEndian, &channels)).To(Succeed())
		Expect(channels).To(Equal(uint16(2)))
		var sampleRate uint32
		Expect(binary.Read(bytes.NewReader(h[24:28]), binary.LittleEndian, &sampleRate)).To(Succeed())
		Expect(sampleRate).To(Equal(uint32(48000)))
	})
	It("falls back to 48 kHz for a non-positive sample rate", func() {
		var sampleRate uint32
		h := wavHeaderStereo(0)
		Expect(binary.Read(bytes.NewReader(h[24:28]), binary.LittleEndian, &sampleRate)).To(Succeed())
		Expect(sampleRate).To(Equal(uint32(48000)))
	})
})

var _ = Describe("floatToPCM16LE", func() {
	It("clamps and converts float PCM to little-endian int16 bytes", func() {
		b := floatToPCM16LE([]float32{0, 1.0, -1.0, 2.0, -2.0})
		Expect(b).To(HaveLen(10))
		read := func(off int) int16 {
			var v int16
			_ = binary.Read(bytes.NewReader(b[off:off+2]), binary.LittleEndian, &v)
			return v
		}
		Expect(read(0)).To(Equal(int16(0)))
		Expect(read(2)).To(Equal(int16(32767)))
		Expect(read(4)).To(Equal(int16(-32767)))
		Expect(read(6)).To(Equal(int16(32767)))  // clamped from 2.0
		Expect(read(8)).To(Equal(int16(-32767))) // clamped from -2.0
	})
})
