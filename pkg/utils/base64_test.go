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
	It("GetContentURIAsBase64 strips data URI prefixes with codec/charset params", func() {
		// Browser MediaRecorder produces data URIs like
		// `data:audio/webm;codecs=opus;base64,...` — the regex must accept
		// any number of MIME parameters between the type and `;base64,`.
		input := "data:audio/webm;codecs=opus;base64,PAYLOAD"
		b64, err := GetContentURIAsBase64(input)
		Expect(err).To(BeNil())
		Expect(b64).To(Equal("PAYLOAD"))
	})
	It("GetImageURLAsBase64 returns an error for bogus data", func() {
		input := "FOO"
		b64, err := GetContentURIAsBase64(input)
		Expect(b64).To(Equal(""))
		Expect(err).ToNot(BeNil())
		Expect(err).To(MatchError("not valid base64 data type string"))
	})
	// The http(s) download branch is exercised hermetically in
	// base64_internal_test.go (white-box, no external network).
})
