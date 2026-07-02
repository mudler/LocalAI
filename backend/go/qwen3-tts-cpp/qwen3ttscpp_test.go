package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQwen3TtsCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qwen3-tts-cpp suite")
}

var _ = Describe("normalizeLanguage", func() {
	DescribeTable("maps caller language to qwentts language names",
		func(in, want string) {
			Expect(normalizeLanguage(in)).To(Equal(want))
		},
		Entry("empty stays empty", "", ""),
		Entry("auto maps to empty", "auto", ""),
		Entry("english full name", "English", "english"),
		Entry("english code", "en", "english"),
		Entry("locale suffix stripped", "en-US", "english"),
		Entry("underscore locale", "zh_CN", "chinese"),
		Entry("mandarin alias", "mandarin", "chinese"),
		Entry("japanese already full", "japanese", "japanese"),
		Entry("unknown passes through normalized", "xx", "xx"),
	)
})

var _ = Describe("resolveVoice", func() {
	It("treats a bare token as a named speaker", func() {
		sp, ref := resolveVoice("serena")
		Expect(sp).To(Equal("serena"))
		Expect(ref).To(BeEmpty())
	})
	It("treats an audio path as a clone reference (case-insensitive ext)", func() {
		sp, ref := resolveVoice("/x/ref.WAV")
		Expect(sp).To(BeEmpty())
		Expect(ref).To(Equal("/x/ref.WAV"))
	})
	It("recognizes mp3/flac/ogg/m4a", func() {
		for _, p := range []string{"a.mp3", "b.flac", "c.ogg", "d.m4a"} {
			sp, ref := resolveVoice(p)
			Expect(sp).To(BeEmpty())
			Expect(ref).To(Equal(p))
		}
	})
	It("returns empty for empty input", func() {
		sp, ref := resolveVoice("  ")
		Expect(sp).To(BeEmpty())
		Expect(ref).To(BeEmpty())
	})
})

var _ = Describe("parseOptions", func() {
	It("extracts codec, use_fa, clamp_fp16, seed", func() {
		o := parseOptions([]string{
			"tokenizer:tok.gguf", "use_fa:false", "clamp_fp16:true",
			"seed:7", "unknown:ignored",
		})
		Expect(o.codecPath).To(Equal("tok.gguf"))
		Expect(o.useFA).To(BeFalse())
		Expect(o.clampFP16).To(BeTrue())
		Expect(o.seed).To(Equal(int64(7)))
	})
	It("accepts codec: as an alias for tokenizer:", func() {
		Expect(parseOptions([]string{"codec:c.gguf"}).codecPath).To(Equal("c.gguf"))
	})
	It("defaults use_fa true and seed -1", func() {
		o := parseOptions(nil)
		Expect(o.useFA).To(BeTrue())
		Expect(o.seed).To(Equal(int64(-1)))
	})
})

var _ = Describe("parseSampling", func() {
	It("applies qt defaults when params are absent", func() {
		s := parseSampling(nil, -1)
		Expect(s.temperature).To(BeNumerically("~", 0.9, 1e-6))
		Expect(s.topK).To(Equal(50))
		Expect(s.topP).To(BeNumerically("~", 1.0, 1e-6))
		Expect(s.repPen).To(BeNumerically("~", 1.05, 1e-6))
		Expect(s.maxNew).To(Equal(2048))
		Expect(s.seed).To(Equal(int64(-1)))
	})
	It("reads overrides and falls back to default seed", func() {
		s := parseSampling(map[string]string{
			"temperature": "0.5", "top_k": "10", "top_p": "0.8",
			"repetition_penalty": "1.2", "max_new_tokens": "512",
		}, 99)
		Expect(s.temperature).To(BeNumerically("~", 0.5, 1e-6))
		Expect(s.topK).To(Equal(10))
		Expect(s.topP).To(BeNumerically("~", 0.8, 1e-6))
		Expect(s.repPen).To(BeNumerically("~", 1.2, 1e-6))
		Expect(s.maxNew).To(Equal(512))
		Expect(s.seed).To(Equal(int64(99)))
	})
	It("reads an explicit seed override", func() {
		Expect(parseSampling(map[string]string{"seed": "123"}, -1).seed).To(Equal(int64(123)))
	})
})

var _ = Describe("wavHeader24k", func() {
	It("emits a 44-byte streaming WAV header at 24 kHz mono 16-bit", func() {
		h := wavHeader24k()
		Expect(h).To(HaveLen(44))
		Expect(string(h[0:4])).To(Equal("RIFF"))
		Expect(string(h[8:12])).To(Equal("WAVE"))
		Expect(string(h[12:16])).To(Equal("fmt "))
		Expect(string(h[36:40])).To(Equal("data"))
		var sampleRate uint32
		Expect(binary.Read(bytes.NewReader(h[24:28]), binary.LittleEndian, &sampleRate)).To(Succeed())
		Expect(sampleRate).To(Equal(uint32(24000)))
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
