package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseOptions", func() {
	It("parses every known key", func() {
		o := parseOptions([]string{
			"vad_model:silero.gguf",
			"pnc_model:pnc.gguf",
			"diar_model:sortformer.gguf",
			"itn_dir:tn_configs",
			"language_code:es-ES",
			"codec_model:nanocodec.gguf",
			"tokenizer_dir:extracted",
			"tn_dir:tn",
			"source_language:en",
			"target_language:de",
		}, "/models")

		Expect(o.vadModel).To(Equal("/models/silero.gguf"))
		Expect(o.pncModel).To(Equal("/models/pnc.gguf"))
		Expect(o.diarModel).To(Equal("/models/sortformer.gguf"))
		Expect(o.itnDir).To(Equal("/models/tn_configs"))
		Expect(o.languageCode).To(Equal("es-ES"))
		Expect(o.codecModel).To(Equal("/models/nanocodec.gguf"))
		Expect(o.tokenizerDir).To(Equal("/models/extracted"))
		Expect(o.tnDir).To(Equal("/models/tn"))
		Expect(o.sourceLanguage).To(Equal("en"))
		Expect(o.targetLanguage).To(Equal("de"))
	})

	It("leaves absolute paths untouched", func() {
		o := parseOptions([]string{"vad_model:/abs/silero.gguf"}, "/models")
		Expect(o.vadModel).To(Equal("/abs/silero.gguf"))
	})

	It("ignores unknown keys and entries without a separator", func() {
		o := parseOptions([]string{"nonsense", "unknown_key:value"}, "/models")
		Expect(o).To(Equal(loadOptions{gpu: -1}))
	})

	It("trims whitespace around keys and values", func() {
		o := parseOptions([]string{"  language_code : en-US  "}, "/models")
		Expect(o.languageCode).To(Equal("en-US"))
	})

	It("keeps a value containing a colon intact", func() {
		// URIs must survive the split on the FIRST colon.
		o := parseOptions([]string{"tokenizer_dir:/a/b:c"}, "/models")
		Expect(o.tokenizerDir).To(Equal("/a/b:c"))
	})

	It("leaves an empty value empty so callers can detect unset", func() {
		o := parseOptions([]string{"vad_model:"}, "/models")
		Expect(o.vadModel).To(BeEmpty())
	})

	It("defaults gpu to -1 meaning CPU", func() {
		o := parseOptions(nil, "/models")
		Expect(o.gpu).To(Equal(int32(-1)))
	})

	It("parses an explicit gpu index", func() {
		o := parseOptions([]string{"gpu:0"}, "/models")
		Expect(o.gpu).To(Equal(int32(0)))
	})
})
