package utils_test

import (
	. "github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("utils/base64 tests", func() {
	It("GetImageURLAsBase64 can strip jpeg data url prefixes", func() {
		// This one doesn't actually _care_ that it's base64, so feed "bad" data in this test in order to catch a change in that behavior for informational purposes.
		input := "data:image/jpeg;base64,FOO"
		b64, err := GetContentURIAsBase64(input)
		Expect(err).To(BeNil())
		Expect(b64).To(Equal("FOO"))
	})
	It("GetImageURLAsBase64 can strip png data url prefixes", func() {
		// This one doesn't actually _care_ that it's base64, so feed "bad" data in this test in order to catch a change in that behavior for informational purposes.
		input := "data:image/png;base64,BAR"
		b64, err := GetContentURIAsBase64(input)
		Expect(err).To(BeNil())
		Expect(b64).To(Equal("BAR"))
	})
	It("GetImageURLAsBase64 returns an error for bogus data", func() {
		input := "FOO"
		b64, err := GetContentURIAsBase64(input)
		Expect(b64).To(Equal(""))
		Expect(err).ToNot(BeNil())
		Expect(err).To(MatchError("not valid string"))
	})
	It("GetImageURLAsBase64 can actually download images and calculates something", func() {
		// This test doesn't actually _check_ the results at this time, which is bad, but there wasn't a test at all before...
		input := "https://upload.wikimedia.org/wikipedia/en/2/29/Wargames.jpg"
		b64, err := GetContentURIAsBase64(input)
		Expect(err).To(BeNil())
		Expect(b64).ToNot(BeNil())
	})
})
