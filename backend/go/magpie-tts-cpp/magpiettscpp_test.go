package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMagpieTtsCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "magpie-tts-cpp suite")
}

var _ = Describe("resolveSpeaker", func() {
	It("canonicalizes case-insensitive names", func() {
		for in, want := range map[string]string{
			"aria": "Aria", "JASON": "Jason", "john": "John",
			"Leo": "Leo", "sofia": "Sofia",
		} {
			got, err := resolveSpeaker(in, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		}
	})
	It("accepts indices 0-4", func() {
		for in, want := range map[string]string{
			"0": "Aria", "1": "Jason", "2": "John", "3": "Leo", "4": "Sofia",
		} {
			got, err := resolveSpeaker(in, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		}
	})
	It("selects the engine default on empty", func() {
		got, err := resolveSpeaker("", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
	It("falls back to the model-level default speaker", func() {
		got, err := resolveSpeaker("", "sofia")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("Sofia"))
	})
	It("prefers the request voice over the fallback", func() {
		got, err := resolveSpeaker("leo", "sofia")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("Leo"))
	})
	It("rejects out-of-range indices", func() {
		_, err := resolveSpeaker("5", "")
		Expect(err).To(HaveOccurred())
		_, err = resolveSpeaker("-1", "")
		Expect(err).To(HaveOccurred())
	})
	It("rejects unknown names with the valid choices", func() {
		_, err := resolveSpeaker("serena", "")
		Expect(err).To(MatchError(ContainSubstring("Aria, Jason, John, Leo, Sofia")))
	})
})

var _ = Describe("resolveLanguage", func() {
	strp := func(s string) *string { return &s }

	It("defaults to empty (engine picks en)", func() {
		Expect(resolveLanguage(nil, "")).To(BeEmpty())
	})
	It("canonicalizes case for known codes", func() {
		Expect(resolveLanguage(strp("EN"), "")).To(Equal("en"))
		Expect(resolveLanguage(strp("pt-br"), "")).To(Equal("pt-BR"))
		Expect(resolveLanguage(strp("AR-MSA"), "")).To(Equal("ar-MSA"))
	})
	It("falls back to the model-level default language", func() {
		Expect(resolveLanguage(nil, "de")).To(Equal("de"))
		Expect(resolveLanguage(strp(""), "PT-BR")).To(Equal("pt-BR"))
	})
	It("prefers the request language over the fallback", func() {
		Expect(resolveLanguage(strp("it"), "de")).To(Equal("it"))
	})
	It("passes unknown codes through verbatim", func() {
		Expect(resolveLanguage(strp("zh"), "")).To(Equal("zh"))
	})
})

var _ = Describe("parseOptions", func() {
	It("reads speaker and language defaults", func() {
		o := parseOptions([]string{"speaker:Jason", "language:de"})
		Expect(o.speaker).To(Equal("Jason"))
		Expect(o.language).To(Equal("de"))
	})
	It("accepts the voice/lang aliases and ignores unknown keys", func() {
		o := parseOptions([]string{"voice: sofia ", "lang: pt-BR", "bogus:1", "novalue"})
		Expect(o.speaker).To(Equal("sofia"))
		Expect(o.language).To(Equal("pt-BR"))
	})
})

var _ = Describe("audio encoding", func() {
	It("emits a well-formed streaming mono WAV header", func() {
		h := wavHeaderMono()
		Expect(h).To(HaveLen(44))
		Expect(string(h[0:4])).To(Equal("RIFF"))
		Expect(string(h[8:12])).To(Equal("WAVE"))
		// channels (offset 22) == 1, sample rate (offset 24) == 22050
		Expect(int(h[22]) | int(h[23])<<8).To(Equal(1))
		Expect(int(h[24]) | int(h[25])<<8 | int(h[26])<<16 | int(h[27])<<24).To(Equal(22050))
	})
	It("clamps float PCM to int16", func() {
		b := floatToPCM16LE([]float32{2, -2, 0})
		Expect(b).To(HaveLen(6))
		Expect(int16(uint16(b[0]) | uint16(b[1])<<8)).To(Equal(int16(32767)))
		Expect(int16(uint16(b[2]) | uint16(b[3])<<8)).To(Equal(int16(-32767)))
		Expect(int16(uint16(b[4]) | uint16(b[5])<<8)).To(Equal(int16(0)))
	})
})
