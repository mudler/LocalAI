package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression for #11097: /v1/audio/speech accepted the documented OpenAI
// `speed` field, dropped it before the request reached the backend, and
// returned 200 with an unchanged playback rate. The field must now be
// normalised onto the params map that is forwarded to the gRPC TTSRequest,
// and an out-of-range value must be rejected instead of silently ignored.
var _ = Describe("applyTTSSpeed", func() {
	It("forwards speed onto the backend params map", func() {
		input := &schema.TTSRequest{Speed: 0.8}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(HaveKeyWithValue("speed", "0.8"))
	})

	It("leaves params untouched when speed is unset", func() {
		input := &schema.TTSRequest{}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(BeNil())
	})

	It("keeps an explicit params entry over the OpenAI field", func() {
		input := &schema.TTSRequest{Speed: 0.8, Params: map[string]string{"speed": "1.5"}}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(HaveKeyWithValue("speed", "1.5"))
	})

	It("preserves unrelated params", func() {
		input := &schema.TTSRequest{Speed: 2, Params: map[string]string{"ref_text": "hello"}}
		Expect(applyTTSSpeed(input)).To(Succeed())
		Expect(input.Params).To(HaveKeyWithValue("ref_text", "hello"))
		Expect(input.Params).To(HaveKeyWithValue("speed", "2"))
	})

	DescribeTable("rejects values outside the OpenAI range",
		func(speed float32) {
			input := &schema.TTSRequest{Speed: speed}
			err := applyTTSSpeed(input)
			Expect(err).To(HaveOccurred())
			httpErr, ok := err.(*echo.HTTPError)
			Expect(ok).To(BeTrue())
			Expect(httpErr.Code).To(Equal(http.StatusBadRequest))
			Expect(input.Params).To(BeNil())
		},
		Entry("below the minimum", float32(0.1)),
		Entry("above the maximum", float32(4.5)),
		Entry("negative", float32(-1)),
	)

	DescribeTable("accepts the documented bounds",
		func(speed float32, want string) {
			input := &schema.TTSRequest{Speed: speed}
			Expect(applyTTSSpeed(input)).To(Succeed())
			Expect(input.Params).To(HaveKeyWithValue("speed", want))
		},
		Entry("minimum", float32(0.25), "0.25"),
		Entry("maximum", float32(4), "4"),
	)
})
