package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Transcription language/translate resolution", func() {
	Describe("resolveTranscriptionLanguage", func() {
		It("prefers the request form field over everything", func() {
			Expect(resolveTranscriptionLanguage("en", "fr", "ru")).To(Equal("en"))
		})

		It("falls back to the parsed request when the form field is empty", func() {
			Expect(resolveTranscriptionLanguage("", "fr", "ru")).To(Equal("fr"))
		})

		It("falls back to the model config default when nothing else is set", func() {
			// The reporter set parameters.language: ru in the model YAML but sent
			// no language form field; the config value must be honored. (#10655)
			Expect(resolveTranscriptionLanguage("", "", "ru")).To(Equal("ru"))
		})

		It("returns empty when no source provides a language", func() {
			Expect(resolveTranscriptionLanguage("", "", "")).To(Equal(""))
		})
	})

	Describe("resolveTranscriptionTranslate", func() {
		It("honors a request form field of true", func() {
			Expect(resolveTranscriptionTranslate("true", false)).To(BeTrue())
		})

		It("honors a request form field of false over a config default of true", func() {
			Expect(resolveTranscriptionTranslate("false", true)).To(BeFalse())
		})

		It("falls back to the model config default when the form field is absent", func() {
			// parameters.translate: true in the YAML must be honored when no form
			// field overrides it.
			Expect(resolveTranscriptionTranslate("", true)).To(BeTrue())
		})

		It("defaults to the config value (false) when neither is set", func() {
			Expect(resolveTranscriptionTranslate("", false)).To(BeFalse())
		})
	})
})
