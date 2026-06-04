package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLanguageNormalization(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qwen3-tts-cpp language normalization")
}

var _ = Describe("normalizeLanguage", func() {
	DescribeTable("maps caller input to the canonical model language code",
		func(input, expected string) {
			Expect(normalizeLanguage(input)).To(Equal(expected))
		},
		// Canonical codes pass through unchanged
		Entry("canonical en", "en", "en"),
		Entry("canonical zh", "zh", "zh"),
		Entry("canonical pt", "pt", "pt"),

		// Case-insensitive
		Entry("uppercase", "EN", "en"),
		Entry("mixed case", "Ja", "ja"),

		// Surrounding whitespace
		Entry("trims whitespace", "  en  ", "en"),

		// Region/locale stripping
		Entry("BCP-47 region", "en-US", "en"),
		Entry("underscore region", "en_US", "en"),
		Entry("dotted locale", "ja.JP", "ja"),
		Entry("region + case", "ZH-CN", "zh"),

		// Full-name aliases
		Entry("english name", "english", "en"),
		Entry("chinese name cased", "Chinese", "zh"),
		Entry("japanese name", "japanese", "ja"),
		Entry("russian name", "russian", "ru"),
		Entry("portuguese name", "portuguese", "pt"),

		// Empty stays empty (C++ applies the English default)
		Entry("empty", "", ""),
		Entry("whitespace only", "   ", ""),

		// Unknown values pass through normalized so C++ can log + default
		Entry("unknown code", "klingon", "klingon"),
		Entry("unknown with region", "xx-YY", "xx"),
	)
})
