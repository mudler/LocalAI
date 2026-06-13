package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOmnivoiceCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "omnivoice-cpp suite")
}

var _ = Describe("normalizeLanguage", func() {
	DescribeTable("maps caller language to OmniVoice codes",
		func(in, want string) {
			Expect(normalizeLanguage(in)).To(Equal(want))
		},
		Entry("empty stays empty", "", ""),
		Entry("english full name", "English", "en"),
		Entry("chinese full name", "Chinese", "zh"),
		Entry("locale suffix stripped", "en-US", "en"),
		Entry("underscore locale", "zh_CN", "zh"),
		Entry("already a code", "en", "en"),
		Entry("unknown passes through normalized", "xx", "xx"),
	)
})

var _ = Describe("parseOptions", func() {
	It("extracts codec, use_fa, clamp_fp16, seed, denoise", func() {
		o := parseOptions([]string{
			"tokenizer:tok.gguf",
			"use_fa:true",
			"clamp_fp16:true",
			"seed:7",
			"denoise:false",
			"unknown:ignored",
		})
		Expect(o.codecPath).To(Equal("tok.gguf"))
		Expect(o.useFA).To(BeTrue())
		Expect(o.clampFP16).To(BeTrue())
		Expect(o.seed).To(Equal(int64(7)))
		Expect(o.denoise).To(BeFalse())
	})

	It("accepts codec: as an alias for tokenizer:", func() {
		o := parseOptions([]string{"codec:c.gguf"})
		Expect(o.codecPath).To(Equal("c.gguf"))
	})

	It("defaults seed to -1 and denoise to true", func() {
		o := parseOptions(nil)
		Expect(o.seed).To(Equal(int64(-1)))
		Expect(o.denoise).To(BeTrue())
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
		Expect(b).To(HaveLen(10)) // 5 samples * 2 bytes
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
